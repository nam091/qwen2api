package openai

import (
	"encoding/json"
	"testing"
)

func TestChatMessageText(t *testing.T) {
	cases := map[string]string{
		`"hello"`: "hello",
		`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`: "ab",
		`[{"type":"image_url","image_url":{"url":"x"}}]`:          "",
		`""`: "",
		// Responses API (codex CLI) wraps user/developer prompts as
		// `input_text` and prior assistant turns as `output_text`. Both must
		// flow through to upstream Qwen.
		`[{"type":"input_text","text":"hi"}]`:                            "hi",
		`[{"type":"output_text","text":"prev"}]`:                         "prev",
		`[{"type":"input_text","text":"a"},{"type":"output_text","text":"b"}]`: "ab",
	}
	for in, want := range cases {
		m := ChatMessage{Role: "user", Content: json.RawMessage(in)}
		if got := m.Text(); got != want {
			t.Errorf("Text(%s) = %q want %q", in, got, want)
		}
	}
}
