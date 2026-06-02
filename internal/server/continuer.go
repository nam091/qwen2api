package server

import (
	"context"
	"io"
	"strings"

	"github.com/keaume34/qwen2api/internal/qwen"
	"github.com/keaume34/qwen2api/internal/toolcall"
)

// qwenContinuer adapt qwen.Client sang toolcall.Continuer.
// LƯU Ý: khớp lại tên field (Messages/Message.Role/Content) với type
// CompletionRequest / Message thực tế trong internal/qwen.
type qwenContinuer struct {
	client  *qwen.Client
	token   string
	chatID  string
	baseReq qwen.CompletionRequest // request gốc đã build sẵn messages
}

func (q *qwenContinuer) ContinueOnce(ctx context.Context, priorText string) (string, error) {
	req := q.baseReq
	req.ChatID = q.chatID

	// Nối assistant turn dở dang + chỉ thị viết tiếp.
	msgs := append([]qwen.Message{}, q.baseReq.Messages...)
	msgs = append(msgs,
		qwen.Message{
			Role:    "assistant",
			Content: priorText,
			Extra:   map[string]interface{}{},
			FeatureConfig: &qwen.FeatureConfig{
				ThinkingEnabled: false,
			},
		},
		qwen.Message{
			Role:    "user",
			Content: toolcall.ContinuationPrompt,
			Extra:   map[string]interface{}{},
			FeatureConfig: &qwen.FeatureConfig{
				ThinkingEnabled: false,
			},
		},
	)
	req.Messages = msgs

	body, err := q.client.Completions(ctx, q.token, req)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	return drainStreamToText(body), nil
}

// drainStreamToText gom toàn bộ delta content của một SSE stream thành text.
// Chỉ lấy content ở phase "answer" hoặc không có phase — bỏ qua thinking
// content (phase "think") để tránh lẫn <think>...</think> vào JSON tool call.
func drainStreamToText(r io.Reader) string {
	reader := qwen.NewStreamReader(r)
	var sb strings.Builder
	for {
		evt, err := reader.Next()
		if err != nil || evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			continue
		}
		delta := evt.Delta.Choices[0].Delta
		// Skip thinking content — only collect answer-phase text.
		if delta.Phase == "think" {
			continue
		}
		sb.WriteString(delta.Content)
	}
	return sb.String()
}
