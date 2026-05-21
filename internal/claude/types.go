// Package claude defines Anthropic Claude Messages API types and conversion
// to/from OpenAI format for qwen2api.
package claude

import "encoding/json"

// MessagesRequest is the Claude Messages API request format.
type MessagesRequest struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	System        string          `json:"system,omitempty"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
}

// Message is a single message in the conversation.
type Message struct {
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
}

// ContentPart can be text, image, or tool_use/tool_result.
type ContentPart struct {
	Type string `json:"type"`

	// For type="text"
	Text string `json:"text,omitempty"`

	// For type="image"
	Source *ImageSource `json:"source,omitempty"`

	// For type="tool_use"
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For type="tool_result"
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ImageSource describes an image in Claude format.
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// Tool describes a function the model may call.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// MessagesResponse is the non-streaming response.
type MessagesResponse struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	Role         string        `json:"role"`
	Content      []ContentPart `json:"content"`
	Model        string        `json:"model"`
	StopReason   string        `json:"stop_reason,omitempty"`
	StopSequence string        `json:"stop_sequence,omitempty"`
	Usage        Usage         `json:"usage"`
}

// Usage carries token accounting.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// StreamEvent is one SSE event in streaming mode.
type StreamEvent struct {
	Type string `json:"type"`

	// For message_start
	Message *MessagesResponse `json:"message,omitempty"`

	// For content_block_start
	Index        int          `json:"index,omitempty"`
	ContentBlock *ContentPart `json:"content_block,omitempty"`

	// For content_block_delta
	Delta *ContentDelta `json:"delta,omitempty"`

	// For message_delta
	Delta2 *MessageDelta `json:"delta,omitempty"`
	Usage  *Usage        `json:"usage,omitempty"`
}

// ContentDelta is incremental content in streaming.
type ContentDelta struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	PartialJSON  string          `json:"partial_json,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	StopReason   string          `json:"stop_reason,omitempty"`
	StopSequence string          `json:"stop_sequence,omitempty"`
}

// MessageDelta carries stop_reason in message_delta events.
type MessageDelta struct {
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
}

// ErrorResponse is Claude's error format.
type ErrorResponse struct {
	Type  string     `json:"type"`
	Error ErrorBody  `json:"error"`
}

// ErrorBody describes the error.
type ErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
