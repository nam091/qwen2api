package openai

import (
	"encoding/json"
	"testing"
)

func TestChatMessage_Images(t *testing.T) {
	cases := map[string][]string{
		// OpenAI nested-object form
		`[{"type":"image_url","image_url":{"url":"https://a/x.png"}}]`: {"https://a/x.png"},
		// Codex string image_url
		`[{"type":"input_image","image_url":"data:image/png;base64,AAA"}]`: {"data:image/png;base64,AAA"},
		// Codex with input_image object
		`[{"type":"input_image","input_image":{"url":"https://b/y.jpg"}}]`: {"https://b/y.jpg"},
		// Qwen-native string
		`[{"type":"image","image":"https://c/z.webp"}]`: {"https://c/z.webp"},
		// Mixed text + image
		`[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"data:image/png;base64,QQ"}]`: {"data:image/png;base64,QQ"},
	}
	for in, want := range cases {
		m := ChatMessage{Role: "user", Content: json.RawMessage(in)}
		got := m.Images()
		if len(got) != len(want) {
			t.Errorf("Images(%s) = %v want %v", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("Images(%s)[%d] = %q want %q", in, i, got[i], want[i])
			}
		}
	}
}

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
