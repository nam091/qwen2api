package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/keaume34/qwen2api/internal/claude"
	"github.com/keaume34/qwen2api/internal/openai"
)

func (h *handlers) claudeMessages(w http.ResponseWriter, r *http.Request) {
	defer func() {
		_ = r.Body.Close()
	}()

	var req claude.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
		return
	}

	if req.Model == "" {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if len(req.Messages) == 0 {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "messages must not be empty")
		return
	}
	if req.MaxTokens == 0 {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "max_tokens is required")
		return
	}

	// Convert Claude request to OpenAI format
	oaiReq, err := claude.ToOpenAI(req)
	if err != nil {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "failed to convert request: "+err.Error())
		return
	}

	// Resolve model alias
	oaiReq.Model = h.deps.Config.ResolveModel(oaiReq.Model)

	// Build upstream request
	upstreamReq := buildQwenRequestWithOptions(oaiReq, h.deps.Config.Features.Multimodal)

	// Get token and call upstream (reuse existing logic)
	token, err := h.deps.TokenPool.Take()
	if err != nil {
		writeClaudeError(w, http.StatusServiceUnavailable, "overloaded_error", "no Qwen token available")
		return
	}

	chatID, err := h.deps.Qwen.NewChat(r.Context(), token.Value, upstreamReq.Model, upstreamReq.ChatType)
	if err != nil {
		writeClaudeError(w, http.StatusBadGateway, "api_error", "failed to create chat: "+err.Error())
		return
	}

	upstreamReq.ChatID = chatID
	body, err := h.deps.Qwen.Completions(r.Context(), token.Value, upstreamReq)
	if err != nil {
		writeClaudeError(w, http.StatusBadGateway, "api_error", "upstream request failed: "+err.Error())
		return
	}
	defer body.Close()

	if req.Stream {
		h.streamClaudeResponse(w, body, oaiReq, upstreamReq.ID)
	} else {
		h.aggregateClaudeResponse(w, body, oaiReq, upstreamReq.ID)
	}
}

func (h *handlers) streamClaudeResponse(w http.ResponseWriter, body io.ReadCloser, req openai.ChatRequest, id string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeClaudeError(w, http.StatusInternalServerError, "api_error", "streaming not supported")
		return
	}

	scanner := bufio.NewScanner(body)
	isFirst := true

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openai.StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Convert to Claude events
		events := claude.StreamChunkToClaude(chunk, isFirst)
		isFirst = false

		for _, event := range events {
			eventJSON, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, string(eventJSON))
			flusher.Flush()
		}
	}
}

func (h *handlers) aggregateClaudeResponse(w http.ResponseWriter, body io.ReadCloser, req openai.ChatRequest, id string) {
	scanner := bufio.NewScanner(body)
	var fullContent string
	var lastChunk openai.StreamChunk

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openai.StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 {
			fullContent += chunk.Choices[0].Delta.Content
			lastChunk = chunk
		}
	}

	// Build OpenAI completion
	oaiResp := openai.ChatCompletion{
		ID:      id,
		Object:  "chat.completion",
		Created: lastChunk.Created,
		Model:   lastChunk.Model,
		Choices: []openai.Choice{
			{
				Index: 0,
				Message: openai.ChatMessageOut{
					Role:    "assistant",
					Content: &fullContent,
				},
				FinishReason: "stop",
			},
		},
		Usage: openai.Usage{
			PromptTokens:     0,
			CompletionTokens: 0,
			TotalTokens:      0,
		},
	}

	// Convert to Claude format
	claudeResp := claude.FromOpenAI(oaiResp)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(claudeResp)
}

func writeClaudeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(claude.ErrorResponse{
		Type: "error",
		Error: claude.ErrorBody{
			Type:    errType,
			Message: message,
		},
	})
}
