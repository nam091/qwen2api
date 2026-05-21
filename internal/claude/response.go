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
	var content []ContentPart

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
				Index: 0,
				ContentBlock: &ContentPart{
					Type: "text",
					Text: "",
				},
			})
		}
		// Emit content_block_delta
		events = append(events, StreamEvent{
			Type:  "content_block_delta",
			Index: 0,
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
				Index: i + 1,
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
				Index: i + 1,
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
				Index: 0,
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
