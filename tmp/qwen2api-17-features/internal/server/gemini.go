package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/keaume34/qwen2api/internal/qwen"
)

// --- Gemini Protocol Types ---

type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// geminiGenerateContent handles Gemini's non-streaming generateContent endpoint.
func (h *handlers) geminiGenerateContent(w http.ResponseWriter, r *http.Request) {
	model := chi.URLParam(r, "model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model parameter required")
		return
	}

	var req geminiRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	messages := geminiToQwenMessages(req.Contents)
	if len(messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "contents must not be empty")
		return
	}

	resolvedModel := h.deps.Config.ResolveModel(model)

	token, err := h.deps.TokenPool.Take()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "no_upstream_token", "no Qwen token available")
		return
	}

	chatID, err := h.deps.Qwen.NewChat(r.Context(), token.Value, resolvedModel, "t2t")
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "failed to create chat: "+err.Error())
		return
	}

	upReq := geminiCompletionReq(chatID, resolvedModel, messages)

	body, err := h.deps.Qwen.Completions(r.Context(), token.Value, upReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "completion failed: "+err.Error())
		return
	}
	defer body.Close()

	fullText := collectSSEText(body)

	resp := geminiResponse{
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Role:  "model",
					Parts: []geminiPart{{Text: fullText}},
				},
				FinishReason: "STOP",
			},
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

// geminiStreamGenerateContent handles Gemini's streaming endpoint.
func (h *handlers) geminiStreamGenerateContent(w http.ResponseWriter, r *http.Request) {
	model := chi.URLParam(r, "model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model parameter required")
		return
	}

	var req geminiRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	messages := geminiToQwenMessages(req.Contents)
	if len(messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "contents must not be empty")
		return
	}

	resolvedModel := h.deps.Config.ResolveModel(model)

	token, err := h.deps.TokenPool.Take()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "no_upstream_token", "no Qwen token available")
		return
	}

	chatID, err := h.deps.Qwen.NewChat(r.Context(), token.Value, resolvedModel, "t2t")
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "failed to create chat: "+err.Error())
		return
	}

	upReq := geminiCompletionReq(chatID, resolvedModel, messages)

	body, err := h.deps.Qwen.Completions(r.Context(), token.Value, upReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "completion failed: "+err.Error())
		return
	}
	defer body.Close()

	// Stream as Gemini SSE
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	reader := qwen.NewStreamReader(body)
	for {
		evt, err := reader.Next()
		if err != nil {
			break
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			continue
		}
		for _, c := range evt.Delta.Choices {
			if c.Delta.Content == "" {
				continue
			}
			chunk := geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Role:  "model",
							Parts: []geminiPart{{Text: c.Delta.Content}},
						},
					},
				},
			}
			chunkJSON, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", chunkJSON)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// geminiToQwenMessages converts Gemini contents to a single collapsed Qwen
// user message. Upstream chat.qwen.ai only accepts one user message per request.
func geminiToQwenMessages(contents []geminiContent) []qwen.Message {
	var sb strings.Builder
	for _, c := range contents {
		role := c.Role
		switch role {
		case "model":
			role = "assistant"
		case "":
			role = "user"
		}
		for _, p := range c.Parts {
			if p.Text != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				if role != "user" {
					fmt.Fprintf(&sb, "[%s]: %s", role, p.Text)
				} else {
					sb.WriteString(p.Text)
				}
			}
		}
	}
	if sb.Len() == 0 {
		return nil
	}
	return []qwen.Message{{
		Role:     "user",
		Content:  sb.String(),
		ChatType: "t2t",
		Extra:    map[string]interface{}{},
		FeatureConfig: &qwen.FeatureConfig{
			OutputSchema: "phase",
		},
	}}
}

// geminiCompletionReq builds a properly formatted upstream request.
func geminiCompletionReq(chatID, model string, messages []qwen.Message) qwen.CompletionRequest {
	return qwen.CompletionRequest{
		ChatID:            chatID,
		Model:             model,
		ChatType:          "t2t",
		SubChatType:       "t2t",
		ChatMode:          "normal",
		Messages:          messages,
		Stream:            true,
		IncrementalOutput: true,
		SessionID:         uuid.NewString(),
		ID:                uuid.NewString(),
	}
}

// collectSSEText aggregates all delta content from SSE events.
func collectSSEText(body io.Reader) string {
	reader := qwen.NewStreamReader(body)
	var sb strings.Builder
	for {
		evt, err := reader.Next()
		if err != nil {
			break
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			continue
		}
		sb.WriteString(evt.Delta.Choices[0].Delta.Content)
	}
	return sb.String()
}

// geminiListModels returns models in Gemini format.
func (h *handlers) geminiListModels(w http.ResponseWriter, r *http.Request) {
	token, err := h.deps.TokenPool.Take()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "no_upstream_token", "no token available")
		return
	}

	models, err := h.deps.Qwen.Models(r.Context(), token.Value)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}

	type geminiModel struct {
		Name                       string   `json:"name"`
		Version                    string   `json:"version"`
		DisplayName                string   `json:"displayName"`
		SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	}

	geminiModels := make([]geminiModel, 0, len(models.Data))
	for _, m := range models.Data {
		geminiModels = append(geminiModels, geminiModel{
			Name:                       "models/" + m.ID,
			Version:                    "001",
			DisplayName:                m.Name,
			SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"},
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"models": geminiModels})
}


