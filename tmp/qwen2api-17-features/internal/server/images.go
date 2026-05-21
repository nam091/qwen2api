package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/keaume34/qwen2api/internal/qwen"
)

// imageRequest is the DALL-E compatible request format.
type imageRequest struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
	N      int    `json:"n"`
	Size   string `json:"size"`
}

// imageResponse matches OpenAI's image generation response format.
type imageResponse struct {
	Created int64       `json:"created"`
	Data    []imageData `json:"data"`
}

type imageData struct {
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

var (
	markdownImgRe = regexp.MustCompile(`!\[[^\]]*\]\((https?://[^\s)]+)\)`)
	jsonURLRe     = regexp.MustCompile(`"(?:url|image_url|image)":\s*"(https?://[^"]+)"`)
	cdnURLRe      = regexp.MustCompile(`https?://[^\s<>"')\]]*(?:\.(?:png|jpg|jpeg|gif|webp|bmp|svg))[^\s<>"')\]]*`)
)

// extractImageURLs extracts image URLs from model response text.
func extractImageURLs(text string) []string {
	var urls []string
	seen := make(map[string]struct{})

	add := func(u string) {
		if _, ok := seen[u]; !ok {
			seen[u] = struct{}{}
			urls = append(urls, u)
		}
	}

	for _, m := range markdownImgRe.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	for _, m := range jsonURLRe.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	for _, m := range cdnURLRe.FindAllString(text, -1) {
		add(m)
	}
	return urls
}

func (h *handlers) imageGenerations(w http.ResponseWriter, r *http.Request) {
	var req imageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "prompt is required")
		return
	}
	if req.N <= 0 {
		req.N = 1
	}

	model := h.deps.Config.ResolveModel(req.Model)
	if model == "" || model == "dall-e-3" || model == "dall-e-2" {
		model = "qwen3.6-plus"
	}

	token, err := h.deps.TokenPool.Take()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "no_upstream_token", "no Qwen token available")
		return
	}

	imagePrompt := fmt.Sprintf("请直接生成图片，不要只输出文字描述。如果可以生成图片，请返回可访问的图片链接或包含图片链接的结果。\n\n用户需求：%s", req.Prompt)

	chatID, err := h.deps.Qwen.NewChat(r.Context(), token.Value, model, "t2t")
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "failed to create chat: "+err.Error())
		return
	}

	upReq := qwen.CompletionRequest{
		ChatID:      chatID,
		Model:       model,
		ChatType:    "t2t",
		SubChatType: "t2t",
		ChatMode:    "normal",
		Messages: []qwen.Message{
			{
				Role:     "user",
				Content:  imagePrompt,
				ChatType: "t2t",
				Extra:    map[string]interface{}{},
				FeatureConfig: &qwen.FeatureConfig{
					OutputSchema: "phase",
				},
			},
		},
		Stream:            true,
		IncrementalOutput: true,
		SessionID:         uuid.NewString(),
		ID:                uuid.NewString(),
	}

	body, err := h.deps.Qwen.Completions(r.Context(), token.Value, upReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "completion failed: "+err.Error())
		return
	}
	defer body.Close()

	// Read all SSE content — may contain image URLs or raw JSON with image data.
	raw, err := io.ReadAll(body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "read response failed: "+err.Error())
		return
	}

	rawStr := string(raw)

	// Try to extract text from SSE stream first
	reader := qwen.NewStreamReader(strings.NewReader(rawStr))
	var fullText strings.Builder
	for {
		evt, err := reader.Next()
		if err != nil {
			break
		}
		if evt.Done {
			break
		}
		if evt.Delta != nil {
			for _, c := range evt.Delta.Choices {
				fullText.WriteString(c.Delta.Content)
			}
		}
		// Also check raw line for image URLs that may not be in delta.content
		fullText.WriteString(evt.Raw)
	}
	// Also scan the raw body itself for image URLs (may be outside SSE format)
	fullText.WriteString(rawStr)

	urls := extractImageURLs(fullText.String())
	data := make([]imageData, 0, len(urls))
	for _, u := range urls {
		data = append(data, imageData{URL: u, RevisedPrompt: req.Prompt})
		if len(data) >= req.N {
			break
		}
	}

	if len(data) == 0 {
		data = append(data, imageData{RevisedPrompt: fullText.String()})
	}

	resp := imageResponse{
		Created: time.Now().Unix(),
		Data:    data,
	}
	writeJSON(w, http.StatusOK, resp)
}
