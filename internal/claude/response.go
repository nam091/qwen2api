package claude

import (
	"encoding/json"

	"github.com/keaume34/qwen2api/internal/openai"
)

// FromOpenAI converts OpenAI ChatCompletion to Claude MessagesResponse.
func FromOpenAI(resp openai.ChatCompletion) MessagesResponse {
	if len(resp.Choices) == 0 {
		return MessagesResponse{
			ID:      resp.ID,
			Type:    "message",
			Role:    "assistant",
			Content: []ContentPart{},
			Model:   resp.Model,
			Usage: Usage{
				InputTokens:  resp.Usage.PromptTokens,
				OutputTokens: resp.Usage.CompletionTokens,
			},
		}
	}

	choice := resp.Choices[0]
	content := []ContentPart{}

	// Add text content if present
	if choice.Message.Content != nil && *choice.Message.Content != "" {
		content = append(content, ContentPart{
			Type: "text",
			Text: *choice.Message.Content,
		})
	}

	// Add tool_use blocks if present
	for _, tc := range choice.Message.ToolCalls {
		input := json.RawMessage(tc.Function.Arguments)
		content = append(content, ContentPart{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	// If content is still empty, add an empty text block so Claude clients don't choke
	if len(content) == 0 {
		content = append(content, ContentPart{
			Type: "text",
			Text: "",
		})
	}

	stopReason := ""
	switch choice.FinishReason {
	case "stop":
		stopReason = "end_turn"
	case "length":
		stopReason = "max_tokens"
	case "tool_calls":
		stopReason = "tool_use"
	}

	return MessagesResponse{
		ID:         resp.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      resp.Model,
		StopReason: stopReason,
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}
}

// StreamChunkToClaude converts OpenAI StreamChunk to Claude StreamEvent.
func StreamChunkToClaude(chunk openai.StreamChunk, isFirst bool) []StreamEvent {
	var events []StreamEvent

	if isFirst {
		// Emit message_start event
		events = append(events, StreamEvent{
			Type: "message_start",
			Message: &MessagesResponse{
				ID:    chunk.ID,
				Type:  "message",
				Role:  "assistant",
				Model: chunk.Model,
				Usage: Usage{},
			},
		})
	}

	if len(chunk.Choices) == 0 {
		return events
	}

	choice := chunk.Choices[0]

	// Handle text delta
	if choice.Delta.Content != "" {
		if isFirst {
			// Emit content_block_start for text
			events = append(events, StreamEvent{
				Type:  "content_block_start",
				Index: intPtr(0),
				ContentBlock: &ContentPart{
					Type: "text",
					Text: "",
				},
			})
		}
		// Emit content_block_delta
		events = append(events, StreamEvent{
			Type:  "content_block_delta",
			Index: intPtr(0),
			Delta: &ContentDelta{
				Type: "text_delta",
				Text: choice.Delta.Content,
			},
		})
	}

	// Handle tool_calls delta
	for i, tc := range choice.Delta.ToolCalls {
		if tc.ID != "" {
			// New tool call - emit content_block_start
			events = append(events, StreamEvent{
				Type:  "content_block_start",
				Index: intPtr(i + 1),
				ContentBlock: &ContentPart{
					Type: "tool_use",
					ID:   tc.ID,
					Name: tc.Function.Name,
				},
			})
		}
		if tc.Function.Arguments != "" {
			// Emit input_json_delta
			events = append(events, StreamEvent{
				Type:  "content_block_delta",
				Index: intPtr(i + 1),
				Delta: &ContentDelta{
					Type:        "input_json_delta",
					PartialJSON: tc.Function.Arguments,
				},
			})
		}
	}

	// Handle finish
	if choice.FinishReason != nil {
		stopReason := ""
		switch *choice.FinishReason {
		case "stop":
			stopReason = "end_turn"
		case "length":
			stopReason = "max_tokens"
		case "tool_calls":
			stopReason = "tool_use"
		}

		if stopReason != "" {
			// Emit content_block_stop
			events = append(events, StreamEvent{
				Type:  "content_block_stop",
				Index: intPtr(0),
			})
			// Emit message_delta
			events = append(events, StreamEvent{
				Type: "message_delta",
				Delta2: &MessageDelta{
					StopReason: stopReason,
				},
			})
			// Emit message_stop
			events = append(events, StreamEvent{
				Type: "message_stop",
			})
		}
	}

	return events
}

// StreamConverter statefully translates OpenAI stream chunks to Claude stream events.
type StreamConverter struct {
	messageStarted bool
	textStarted    bool
	toolsStarted   map[int]bool
}

// NewStreamConverter returns a new StreamConverter.
func NewStreamConverter() *StreamConverter {
	return &StreamConverter{
		toolsStarted: make(map[int]bool),
	}
}

// Convert statefully converts an OpenAI StreamChunk into a slice of Claude StreamEvents.
func (sc *StreamConverter) Convert(chunk openai.StreamChunk) []StreamEvent {
	var events []StreamEvent

	if !sc.messageStarted {
		sc.messageStarted = true
		// Emit message_start event
		events = append(events, StreamEvent{
			Type: "message_start",
			Message: &MessagesResponse{
				ID:    chunk.ID,
				Type:  "message",
				Role:  "assistant",
				Model: chunk.Model,
				Usage: Usage{},
			},
		})
	}

	if len(chunk.Choices) == 0 {
		return events
	}

	choice := chunk.Choices[0]

	// Handle text delta
	if choice.Delta.Content != "" {
		if !sc.textStarted {
			sc.textStarted = true
			events = append(events, StreamEvent{
				Type:  "content_block_start",
				Index: intPtr(0),
				ContentBlock: &ContentPart{
					Type: "text",
					Text: "",
				},
			})
		}
		// Emit content_block_delta
		events = append(events, StreamEvent{
			Type:  "content_block_delta",
			Index: intPtr(0),
			Delta: &ContentDelta{
				Type: "text_delta",
				Text: choice.Delta.Content,
			},
		})
	}

	// Handle tool_calls delta
	for _, tc := range choice.Delta.ToolCalls {
		idx := tc.Index
		if !sc.toolsStarted[idx] {
			sc.toolsStarted[idx] = true
			events = append(events, StreamEvent{
				Type:  "content_block_start",
				Index: intPtr(idx),
				ContentBlock: &ContentPart{
					Type: "tool_use",
					ID:   tc.ID,
					Name: tc.Function.Name,
				},
			})
		}
		if tc.Function.Arguments != "" {
			events = append(events, StreamEvent{
				Type:  "content_block_delta",
				Index: intPtr(idx),
				Delta: &ContentDelta{
					Type:        "input_json_delta",
					PartialJSON: tc.Function.Arguments,
				},
			})
		}
	}

	// Handle finish
	if choice.FinishReason != nil {
		stopReason := ""
		switch *choice.FinishReason {
		case "stop":
			stopReason = "end_turn"
		case "length":
			stopReason = "max_tokens"
		case "tool_calls":
			stopReason = "tool_use"
		}

		if stopReason != "" {
			// Emit content_block_stop for text if it started
			if sc.textStarted {
				events = append(events, StreamEvent{
					Type:  "content_block_stop",
					Index: intPtr(0),
				})
			}
			// Emit content_block_stop for any tools that started
			for idx := range sc.toolsStarted {
				events = append(events, StreamEvent{
					Type:  "content_block_stop",
					Index: intPtr(idx),
				})
			}
			// Emit message_delta
			events = append(events, StreamEvent{
				Type: "message_delta",
				Delta2: &MessageDelta{
					StopReason: stopReason,
				},
			})
			// Emit message_stop
			events = append(events, StreamEvent{
				Type: "message_stop",
			})
		}
	}

	return events
}
