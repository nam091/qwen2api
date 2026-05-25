package qwen

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessage_MarshalJSON_PlainContent(t *testing.T) {
	m := Message{
		Role:     "user",
		Content:  "hello",
		ChatType: "t2t",
		Extra:    map[string]interface{}{},
		FeatureConfig: &FeatureConfig{
			OutputSchema:    "phase",
			ThinkingEnabled: false,
		},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	// Plain string form must be emitted.
	if !strings.Contains(got, `"content":"hello"`) {
		t.Fatalf("expected plain content; got %s", got)
	}
	if strings.Contains(got, `"content":[`) {
		t.Fatalf("did not expect array content; got %s", got)
	}
}

func TestMessage_MarshalJSON_ContentParts(t *testing.T) {
	m := Message{
		Role:     "user",
		ChatType: "t2t",
		Extra:    map[string]interface{}{},
		FeatureConfig: &FeatureConfig{
			OutputSchema:    "phase",
			ThinkingEnabled: false,
		},
		ContentParts: []ContentPart{
			{Type: "text", Text: "describe this"},
			{Type: "image", Image: "https://example.com/img.png"},
		},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, `"content":[`) {
		t.Fatalf("expected array content; got %s", got)
	}
	if !strings.Contains(got, `"type":"text"`) || !strings.Contains(got, `"text":"describe this"`) {
		t.Fatalf("expected text part; got %s", got)
	}
	if !strings.Contains(got, `"type":"image"`) || !strings.Contains(got, `"image":"https://example.com/img.png"`) {
		t.Fatalf("expected image part; got %s", got)
	}
	// Must NOT include separate "content" string field.
	if strings.Contains(got, `"content":"`) {
		t.Fatalf("did not expect plain string content alongside array; got %s", got)
	}
}
