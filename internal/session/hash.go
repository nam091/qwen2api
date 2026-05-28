package session

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
)

// HashMessages computes a context hash from the conversation messages.
// All messages except the last are hashed together with the model name.
// This means "same prior context = same session" without explicit IDs.
// Returns a 32-char hex string.
func HashMessages(msgs []openai.ChatMessage, model string) string {
	if len(msgs) <= 1 {
		return ""
	}
	// Hash all messages except the last
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte("||"))
	for _, m := range msgs[:len(msgs)-1] {
		h.Write([]byte(m.Role))
		h.Write([]byte(":"))
		text := m.Text()
		if len(text) > 4000 {
			text = text[:4000]
		}
		h.Write([]byte(text))
		h.Write([]byte("\n"))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16]) // 32 hex chars
}

// FormatMessages turns a slice of session messages into a readable log for
// the summarization prompt.
func FormatMessages(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}
