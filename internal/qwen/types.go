// Package qwen wraps the upstream chat.qwen.ai HTTP/SSE protocol.
package qwen

import "encoding/json"

// Message is a single chat turn sent upstream. Upstream requires `extra`
// (object) and `feature_config` (with `output_schema` and `thinking_enabled`)
// on every message — leaving them out yields a 400 Bad_Request.
type Message struct {
	Role string `json:"role"`
	// Content is the plain-text body. Used when ContentParts is empty.
	Content string `json:"-"`
	// ContentParts, when non-empty, is emitted as the multimodal `content`
	// array (mixing text + image parts). Required for vision input.
	ContentParts []ContentPart `json:"-"`
	// ChatType pins the per-message chat type (Qwen extension). Optional.
	ChatType string `json:"chat_type,omitempty"`
	// Extra is required by upstream (even if empty).
	Extra map[string]interface{} `json:"extra"`
	// FeatureConfig.thinking_enabled toggles the thinking mode for that turn.
	FeatureConfig *FeatureConfig `json:"feature_config"`
}

// ContentPart is one element of a multimodal `content` array sent upstream.
// Upstream expects {type:"text", text:...} or {type:"image", image:URL}.
type ContentPart struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
}

// MarshalJSON renders Content as either a string (when ContentParts is empty)
// or as an array of multimodal parts — matching the wire shape chat.qwen.ai
// expects for vision input.
func (m Message) MarshalJSON() ([]byte, error) {
	type alias Message
	if len(m.ContentParts) == 0 {
		return json.Marshal(struct {
			Content string `json:"content"`
			alias
		}{Content: m.Content, alias: alias(m)})
	}
	return json.Marshal(struct {
		Content []ContentPart `json:"content"`
		alias
	}{Content: m.ContentParts, alias: alias(m)})
}

// FeatureConfig matches the upstream `feature_config` schema.
type FeatureConfig struct {
	ThinkingEnabled bool   `json:"thinking_enabled"`
	OutputSchema    string `json:"output_schema"`
}

// CompletionRequest is the payload sent to /api/v2/chat/completions.
type CompletionRequest struct {
	Stream            bool      `json:"stream"`
	IncrementalOutput bool      `json:"incremental_output"`
	ChatType          string    `json:"chat_type"`
	SubChatType       string    `json:"sub_chat_type"`
	ChatMode          string    `json:"chat_mode"`
	Model             string    `json:"model"`
	Messages          []Message `json:"messages"`
	SessionID         string    `json:"session_id"`
	ID                string    `json:"id"`
	ChatID            string    `json:"chat_id,omitempty"`
}

// NewChatRequest is the payload for /api/v2/chats/new.
type NewChatRequest struct {
	Title     string   `json:"title"`
	Models    []string `json:"models"`
	ChatMode  string   `json:"chat_mode"`
	ChatType  string   `json:"chat_type"`
	Timestamp int64    `json:"timestamp"`
}

// NewChatResponse mirrors the relevant fields of the upstream /chats/new reply.
type NewChatResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

// StreamDelta is one incremental chunk returned by upstream SSE.
type StreamDelta struct {
	Choices []struct {
		Delta struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			// Phase is "answer" or "think" — Qwen embeds reasoning content in a separate phase.
			Phase string `json:"phase,omitempty"`
			Name  string `json:"name,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

// Usage carries upstream token accounting when reported.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ModelsResponse mirrors GET /api/models.
type ModelsResponse struct {
	Data []ModelInfo `json:"data"`
}

// ModelInfo is one entry from /api/models.
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}
