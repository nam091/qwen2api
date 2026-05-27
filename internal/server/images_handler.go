package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/qwen"
)

func (h *handlers) imageGenerations(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()

	var req openai.ImageGenerationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
		return
	}
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "prompt is required")
		return
	}
	if req.N <= 0 {
		req.N = 1
	}
	if req.N > 10 {
		req.N = 10
	}
	rf := strings.ToLower(strings.TrimSpace(req.ResponseFormat))
	if rf == "" {
		rf = "url"
	}
	if rf != "url" && rf != "b64_json" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "response_format must be url or b64_json")
		return
	}

	model := h.deps.Config.ResolveModel(req.Model)
	if model == "" {
		model = "qwen3.5-plus"
	}

	ratio := mapSizeToQwenRatio(req.Size)

	token, err := h.deps.TokenPool.Take()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "no_tokens", "no available tokens")
		return
	}

	ctx := r.Context()

	chatID, err := h.deps.Qwen.NewChat(ctx, token.Value, model, "t2i")
	if err != nil {
		h.handleUpstreamFailure(w, token.Value, err, "new_chat")
		return
	}

	prompt := req.Prompt
	if req.N > 1 {
		prompt = fmt.Sprintf("%s\n\n(Generate %d images.)", prompt, req.N)
	}

	completionReq := qwen.CompletionRequest{
		Stream:            true,
		IncrementalOutput: true,
		ChatType:          "t2i",
		SubChatType:       "t2i",
		ChatMode:          "normal",
		Model:             model,
		ChatID:            chatID,
		Size:              ratio,
		Messages: []qwen.Message{
			{
				Role:     "user",
				Content:  prompt,
				ChatType: "t2i",
				Extra:    map[string]interface{}{"meta": map[string]interface{}{"subChatType": "t2i"}},
				FeatureConfig: &qwen.FeatureConfig{
					OutputSchema:    "phase",
					ThinkingEnabled: true,
				},
			},
		},
	}

	body, err := h.deps.Qwen.Completions(ctx, token.Value, completionReq)
	if err != nil {
		h.handleUpstreamFailure(w, token.Value, err, "completions")
		return
	}
	defer func() { _ = body.Close() }()

	urls := extractImageGenURLs(body)
	if len(urls) == 0 {
		writeError(w, http.StatusBadGateway, "upstream_error", "upstream returned no image URLs")
		return
	}
	if len(urls) > req.N {
		urls = urls[:req.N]
	}

	resp := openai.ImageGenerationResponse{
		Created: int(unixNow()),
	}

	if rf == "b64_json" {
		for _, u := range urls {
			b64, err := fetchImageBase64(u)
			if err != nil {
				writeError(w, http.StatusBadGateway, "upstream_error", "failed to fetch image: "+err.Error())
				return
			}
			resp.Data = append(resp.Data, openai.ImageGenerationDatum{B64JSON: b64})
		}
	} else {
		for _, u := range urls {
			resp.Data = append(resp.Data, openai.ImageGenerationDatum{URL: u})
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// mapSizeToQwenRatio converts an OpenAI-style size string to a Qwen ratio.
// Accepts direct ratios ("1:1", "16:9") or pixel dimensions ("1024x1024").
func mapSizeToQwenRatio(size string) string {
	s := strings.TrimSpace(size)
	if s == "" {
		return "1:1"
	}

	// Try direct ratio format first.
	validRatios := map[string]bool{
		"1:1": true, "16:9": true, "9:16": true, "4:3": true, "3:4": true,
	}
	if validRatios[s] {
		return s
	}

	// Try "WxH" pixel format.
	if parts := strings.SplitN(s, "x", 2); len(parts) == 2 {
		w, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		h, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err1 == nil && err2 == nil && h > 0 {
			r := w / h
			candidates := []struct {
				key string
				r   float64
			}{
				{"1:1", 1.0},
				{"16:9", 16.0 / 9.0},
				{"9:16", 9.0 / 16.0},
				{"4:3", 4.0 / 3.0},
				{"3:4", 3.0 / 4.0},
			}
			best := candidates[0]
			bestDiff := math.Abs(r - candidates[0].r)
			for _, c := range candidates[1:] {
				if d := math.Abs(r - c.r); d < bestDiff {
					best = c
					bestDiff = d
				}
			}
			return best.key
		}
	}

	return "1:1"
}

// extractImageGenURLs reads the full SSE stream and extracts image URLs from
// deltas where phase == "image_gen".
func extractImageGenURLs(r io.Reader) []string {
	sr := qwen.NewStreamReader(r)
	var urls []string
	seen := make(map[string]bool)
	for {
		evt, err := sr.Next()
		if err != nil {
			break
		}
		if evt.Done || evt.Delta == nil {
			continue
		}
		if len(evt.Delta.Choices) == 0 {
			continue
		}
		delta := evt.Delta.Choices[0].Delta
		if delta.Phase != "image_gen" {
			continue
		}
		u := strings.TrimSpace(delta.Content)
		if u == "" || !strings.HasPrefix(u, "http") {
			continue
		}
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}

// fetchImageBase64 downloads an image and returns its base64 encoding.
func fetchImageBase64(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
