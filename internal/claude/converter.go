package claude

import (
	"encoding/json"
	"fmt"

	"github.com/keaume34/qwen2api/internal/openai"
)

// ToOpenAI converts a Claude MessagesRequest to OpenAI ChatRequest.
func ToOpenAI(req MessagesRequest) (openai.ChatRequest, error) {
	var messages []openai.ChatMessage

	// Add system message if present
	if string(req.System) != "" {
		messages = append(messages, openai.ChatMessage{
			Role:    "system",
			Content: json.RawMessage(fmt.Sprintf(`"%s"`, escapeJSON(string(req.System)))),
		})
	}

	// Convert Claude messages to OpenAI format
	for _, msg := range req.Messages {
		oaiMsg, err := convertMessage(msg)
		if err != nil {
			return openai.ChatRequest{}, err
		}
		messages = append(messages, oaiMsg)
	}

	// Convert tools
	var tools []openai.Tool
	for _, t := range req.Tools {
		tools = append(tools, openai.Tool{
			Type: "function",
			Function: openai.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return openai.ChatRequest{
		Model:       req.Model,
		Messages:    messages,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   &req.MaxTokens,
		Tools:       tools,
		ToolChoice:  req.ToolChoice,
	}, nil
}

func convertMessage(msg Message) (openai.ChatMessage, error) {
	// Simple text-only message
	if len(msg.Content) == 1 && msg.Content[0].Type == "text" {
		return openai.ChatMessage{
			Role:    msg.Role,
			Content: json.RawMessage(fmt.Sprintf(`"%s"`, escapeJSON(msg.Content[0].Text))),
		}, nil
	}

	// Handle tool_use in assistant messages
	if msg.Role == "assistant" {
		var toolCalls []openai.ToolCall
		var textParts []string

		for _, part := range msg.Content {
			switch part.Type {
			case "text":
				textParts = append(textParts, part.Text)
			case "tool_use":
				toolCalls = append(toolCalls, openai.ToolCall{
					ID:   part.ID,
					Type: "function",
					Function: openai.ToolCallFunction{
						Name:      part.Name,
						Arguments: string(part.Input),
					},
				})
			}
		}

		content := ""
		if len(textParts) > 0 {
			content = textParts[0]
		}

		return openai.ChatMessage{
			Role:      "assistant",
			Content:   json.RawMessage(fmt.Sprintf(`"%s"`, escapeJSON(content))),
			ToolCalls: toolCalls,
		}, nil
	}

	// Handle tool_result in user messages
	if msg.Role == "user" {
		for _, part := range msg.Content {
			if part.Type == "tool_result" {
				return openai.ChatMessage{
					Role:       "tool",
					Content:    json.RawMessage(fmt.Sprintf(`"%s"`, escapeJSON(string(part.Content)))),
					ToolCallID: part.ToolUseID,
				}, nil
			}
		}
	}

	// Multimodal or complex content - convert to array format
	var parts []map[string]interface{}
	for _, part := range msg.Content {
		switch part.Type {
		case "text":
			parts = append(parts, map[string]interface{}{
				"type": "text",
				"text": part.Text,
			})
		case "image":
			if part.Source != nil {
				parts = append(parts, map[string]interface{}{
					"type": "image_url",
					"image_url": map[string]string{
						"url": fmt.Sprintf("data:%s;base64,%s", part.Source.MediaType, part.Source.Data),
					},
				})
			}
		}
	}

	contentJSON, err := json.Marshal(parts)
	if err != nil {
		return openai.ChatMessage{}, err
	}

	return openai.ChatMessage{
		Role:    msg.Role,
		Content: json.RawMessage(contentJSON),
	}, nil
}

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1]) // Strip surrounding quotes
}
