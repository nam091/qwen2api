package claude

import (
	"encoding/json"
	"fmt"
)

// SystemPrompt is a custom type that can unmarshal from either a string or an
// array of text blocks.
type SystemPrompt string

func (sp *SystemPrompt) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*sp = SystemPrompt(str)
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &parts); err == nil {
		var combined string
		for _, p := range parts {
			if p.Type == "text" || p.Type == "" {
				combined += p.Text
			}
		}
		*sp = SystemPrompt(combined)
		return nil
	}
	return fmt.Errorf("system prompt must be a string or an array of content blocks")
}

// MessagesRequest is the Claude Messages API request format.
type MessagesRequest struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	System        SystemPrompt    `json:"system,omitempty"`
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

func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role

	if len(raw.Content) == 0 {
		return nil
	}

	// Try as a plain string first.
	var str string
	if err := json.Unmarshal(raw.Content, &str); err == nil {
		m.Content = []ContentPart{{
			Type: "text",
			Text: str,
		}}
		return nil
	}

	// Try as an array of ContentPart.
	var parts []ContentPart
	if err := json.Unmarshal(raw.Content, &parts); err == nil {
		m.Content = parts
		return nil
	}

	return fmt.Errorf("message content must be a string or an array of content blocks")
}

// ToolResultContent is a custom type that can unmarshal from either a string or an
// array of content blocks (for tool_result blocks).
type ToolResultContent string

func (trc *ToolResultContent) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*trc = ToolResultContent(str)
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &parts); err == nil {
		var combined string
		for _, p := range parts {
			if p.Type == "text" || p.Type == "" {
				combined += p.Text
			}
		}
		*trc = ToolResultContent(combined)
		return nil
	}
	return fmt.Errorf("tool_result content must be a string or an array of content blocks")
}

// ContentPart can be text, image, thinking, or tool_use/tool_result.
type ContentPart struct {
	Type string `json:"type"`

	// For type="text"
	Text string `json:"-"` // custom marshal below

	// For type="thinking"
	Thinking string `json:"thinking,omitempty"`

	// For type="image"
	Source *ImageSource `json:"source,omitempty"`

	// For type="tool_use"
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For type="tool_result"
	ToolUseID string            `json:"tool_use_id,omitempty"`
	Content   ToolResultContent `json:"content,omitempty"`
	IsError   bool              `json:"is_error,omitempty"`
}

// MarshalJSON ensures "text" field is always present for text-type content blocks
// and "thinking" field for thinking-type blocks.
func (cp ContentPart) MarshalJSON() ([]byte, error) {
	type Alias ContentPart
	switch cp.Type {
	case "text":
		return json.Marshal(struct {
			Alias
			Text string `json:"text"`
		}{Alias: Alias(cp), Text: cp.Text})
	case "thinking":
		return json.Marshal(struct {
			Alias
			Thinking string `json:"thinking"`
		}{Alias: Alias(cp), Thinking: cp.Thinking})
	}
	return json.Marshal(struct {
		Alias
	}{Alias: Alias(cp)})
}

// UnmarshalJSON handles the text field which is tagged as json:"-".
func (cp *ContentPart) UnmarshalJSON(data []byte) error {
	type Alias ContentPart
	aux := &struct {
		*Alias
		Text string `json:"text"`
	}{Alias: (*Alias)(cp)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	cp.Text = aux.Text
	return nil
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
	Type string `json:"-"`

	// For message_start
	Message *MessagesResponse `json:"-"`

	// For content_block_start / content_block_stop / content_block_delta
	Index        *int         `json:"-"`
	ContentBlock *ContentPart `json:"-"`

	// For content_block_delta
	Delta *ContentDelta `json:"-"`

	// For message_delta
	Delta2 *MessageDelta `json:"-"`
	Usage  *Usage        `json:"-"`
}

// MarshalJSON produces the correct JSON shape for each event type.
func (e StreamEvent) MarshalJSON() ([]byte, error) {
	switch e.Type {
	case "message_start":
		return json.Marshal(struct {
			Type    string           `json:"type"`
			Message *MessagesResponse `json:"message"`
		}{Type: e.Type, Message: e.Message})

	case "content_block_start":
		idx := 0
		if e.Index != nil {
			idx = *e.Index
		}
		return json.Marshal(struct {
			Type         string       `json:"type"`
			Index        int          `json:"index"`
			ContentBlock *ContentPart `json:"content_block"`
		}{Type: e.Type, Index: idx, ContentBlock: e.ContentBlock})

	case "content_block_delta":
		idx := 0
		if e.Index != nil {
			idx = *e.Index
		}
		return json.Marshal(struct {
			Type  string        `json:"type"`
			Index int           `json:"index"`
			Delta *ContentDelta `json:"delta"`
		}{Type: e.Type, Index: idx, Delta: e.Delta})

	case "content_block_stop":
		idx := 0
		if e.Index != nil {
			idx = *e.Index
		}
		return json.Marshal(struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
		}{Type: e.Type, Index: idx})

	case "message_delta":
		return json.Marshal(struct {
			Type  string       `json:"type"`
			Delta *MessageDelta `json:"delta"`
			Usage *Usage       `json:"usage,omitempty"`
		}{Type: e.Type, Delta: e.Delta2, Usage: e.Usage})

	case "message_stop":
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: e.Type})

	default:
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: e.Type})
	}
}

// intPtr is a helper to create *int values for StreamEvent.Index.
func intPtr(i int) *int { return &i }

// ContentDelta is incremental content in streaming.
type ContentDelta struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
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
