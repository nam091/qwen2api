// Package openai defines the OpenAI Chat Completions wire types that
// qwen2api accepts from clients and emits back to them.
package openai

import "encoding/json"

// ChatRequest mirrors the subset of OpenAI Chat Completions used by qwen2api.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Stream      bool          `json:"stream,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Tools       []Tool        `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
	// Qwen extensions accepted but optional.
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

// ChatMessage allows both plain-string and array (multimodal) content.
type ChatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
}

// Text returns the plain text view of Content. Multimodal arrays are flattened
// by concatenating any `text` parts.
func (m ChatMessage) Text() string {
	if len(m.Content) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	// Try array of parts.
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		out := ""
		for _, p := range parts {
			// Accept "text" (OpenAI chat), "input_text" (Responses API input
			// from clients like codex CLI), and "output_text" (Responses API
			// previous assistant turns echoed back as part of conversation
			// history). Empty Type defaults to text.
			if p.Type == "text" || p.Type == "" || p.Type == "input_text" || p.Type == "output_text" {
				out += p.Text
			}
		}
		return out
	}
	return ""
}

// ContentPart describes one element of a multimodal message body. The
// `image_url` field accepts either OpenAI's nested-object form
// (`{url: "..."}`) or the Codex CLI form where it is a plain string. The
// Qwen-native `image` field (string URL) is also recognised so messages
// echoed back through conversation history don't lose image context.
type ContentPart struct {
	Type     string         `json:"type,omitempty"`
	Text     string         `json:"text,omitempty"`
	ImageURL ContentImageRef `json:"image_url,omitempty"`
	// InputImage is the codex `input_image.url` form (objects with a url
	// field, used by some Responses API clients).
	InputImage ContentImageRef `json:"input_image,omitempty"`
	// Image is the Qwen-native string form: {type:"image", image:"<URL>"}.
	Image string `json:"image,omitempty"`
}

// ContentImageRef holds an image reference that may arrive as either a plain
// string URL or as a nested object with `url` and optional `detail` fields.
type ContentImageRef struct {
	URL    string
	Detail string
}

// UnmarshalJSON accepts both string and object encodings of image refs.
func (r *ContentImageRef) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		r.URL = s
		return nil
	}
	var obj struct {
		URL    string `json:"url"`
		Detail string `json:"detail,omitempty"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	r.URL = obj.URL
	r.Detail = obj.Detail
	return nil
}

// MarshalJSON keeps the nested-object form on the way out for OpenAI clients
// that expect it.
func (r ContentImageRef) MarshalJSON() ([]byte, error) {
	if r.URL == "" {
		return []byte("null"), nil
	}
	if r.Detail == "" {
		return json.Marshal(struct {
			URL string `json:"url"`
		}{URL: r.URL})
	}
	return json.Marshal(struct {
		URL    string `json:"url"`
		Detail string `json:"detail"`
	}{URL: r.URL, Detail: r.Detail})
}

// Parts returns the structured content parts for a multimodal message. Returns
// nil if Content is a plain string.
func (m ChatMessage) Parts() []ContentPart {
	if len(m.Content) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		return parts
	}
	return nil
}

// Images returns image URLs contained in a multimodal message body. Recognises
// OpenAI's `image_url`, Codex's `input_image`, and Qwen-native `image` shapes.
func (m ChatMessage) Images() []string {
	var out []string
	for _, p := range m.Parts() {
		if url := p.ImageRef(); url != "" {
			out = append(out, url)
		}
	}
	return out
}

// ImageRef returns the underlying image URL of a content part, picking from
// whichever field carries it.
func (p ContentPart) ImageRef() string {
	switch p.Type {
	case "image_url", "input_image", "image":
		// fall through to lookup below
	default:
		if p.Type != "" && p.ImageURL.URL == "" && p.InputImage.URL == "" && p.Image == "" {
			return ""
		}
	}
	if p.ImageURL.URL != "" {
		return p.ImageURL.URL
	}
	if p.InputImage.URL != "" {
		return p.InputImage.URL
	}
	return p.Image
}

// ChatCompletion is the non-streaming response envelope.
type ChatCompletion struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is one completion in the non-streaming envelope.
type Choice struct {
	Index        int            `json:"index"`
	Message      ChatMessageOut `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

// ChatMessageOut is the assistant message returned to the client.
type ChatMessageOut struct {
	Role      string     `json:"role"`
	Content   *string    `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Usage carries token accounting.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamChunk is one SSE chunk emitted to the client.
type StreamChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

// StreamChoice is one delta in a stream chunk.
type StreamChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason,omitempty"`
}

// Delta is the incremental content for a stream chunk.
type Delta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
}

// Tool describes a function the model may call.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function schema within a Tool definition.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall is a structured tool invocation in the response.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction carries the name and serialized arguments of a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCallDelta is the incremental streaming form of a tool call.
type ToolCallDelta struct {
	Index    int               `json:"index"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function ToolCallFuncDelta `json:"function,omitempty"`
}

// ToolCallFuncDelta carries incremental name/arguments for streaming.
type ToolCallFuncDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ModelList is the OpenAI /v1/models envelope.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model is one entry of /v1/models.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ErrorEnvelope is the OpenAI-style error response.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody describes the error.
type ErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// ImageGenerationRequest is the OpenAI /v1/images/generations request body.
type ImageGenerationRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	ResponseFormat string `json:"response_format"` // "url" (default) or "b64_json"
}

// ImageGenerationResponse is the OpenAI /v1/images/generations response body.
type ImageGenerationResponse struct {
	Created int                    `json:"created"`
	Data    []ImageGenerationDatum `json:"data"`
}

// ImageGenerationDatum is one image in the generation response.
type ImageGenerationDatum struct {
	URL     string `json:"url,omitempty"`
	B64JSON string `json:"b64_json,omitempty"`
}
