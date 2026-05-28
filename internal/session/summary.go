package session

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/qwen"
)

// GenerateSummary calls upstream Qwen to produce a rolling summary of the
// conversation so far. It is intended to be called asynchronously after
// storing an assistant response.
func GenerateSummary(ctx context.Context, client *qwen.Client, token, model string, msgs []Message, currentSummary string) (string, error) {
	formatted := FormatMessages(msgs)

	var prompt string
	if currentSummary != "" {
		prompt = fmt.Sprintf("Summarize this conversation concisely. Preserve goals, decisions, file paths, open tasks.\n\nPrevious summary:\n%s\n\nRecent messages:\n%s", currentSummary, formatted)
	} else {
		prompt = fmt.Sprintf("Summarize this conversation concisely. Preserve goals, decisions, file paths, open tasks.\n\nRecent messages:\n%s", formatted)
	}

	chatID, err := client.NewChat(ctx, token, model, "t2t")
	if err != nil {
		return "", fmt.Errorf("create summary chat: %w", err)
	}

	req := qwen.CompletionRequest{
		Stream:            true,
		IncrementalOutput: true,
		ChatType:          "t2t",
		SubChatType:       "t2t",
		ChatMode:          "normal",
		Model:             model,
		ChatID:            chatID,
		Messages: []qwen.Message{
			{
				Role:    "user",
				Content: prompt,
				Extra:   map[string]interface{}{},
				FeatureConfig: &qwen.FeatureConfig{
					OutputSchema:    "phase",
					ThinkingEnabled: false,
				},
			},
		},
	}

	body, err := client.Completions(ctx, token, req)
	if err != nil {
		return "", fmt.Errorf("summary completion: %w", err)
	}
	defer func() { _ = body.Close() }()

	return readFullText(body)
}

// readFullText reads the entire SSE stream and returns the concatenated text.
func readFullText(r io.Reader) (string, error) {
	sr := qwen.NewStreamReader(r)
	var b strings.Builder
	for {
		evt, err := sr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return b.String(), err
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			continue
		}
		b.WriteString(evt.Delta.Choices[0].Delta.Content)
	}
	return strings.TrimSpace(b.String()), nil
}

// BuildSummaryPrompt returns the messages to send upstream for summarization
// (exported for testing).
func BuildSummaryPrompt(msgs []Message, currentSummary string) string {
	formatted := FormatMessages(msgs)
	if currentSummary != "" {
		return fmt.Sprintf("Summarize this conversation concisely. Preserve goals, decisions, file paths, open tasks.\n\nPrevious summary:\n%s\n\nRecent messages:\n%s", currentSummary, formatted)
	}
	return fmt.Sprintf("Summarize this conversation concisely. Preserve goals, decisions, file paths, open tasks.\n\nRecent messages:\n%s", formatted)
}

// MessagesFromOpenAI converts OpenAI ChatMessages to session Messages.
func MessagesFromOpenAI(msgs []openai.ChatMessage) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		text := strings.TrimSpace(m.Text())
		if text == "" {
			continue
		}
		out = append(out, Message{
			Role:    m.Role,
			Content: text,
		})
	}
	return out
}
