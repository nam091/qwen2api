package contextoffload

import (
	"strings"
	"testing"
)

func TestEstimateLength(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
	}
	n := EstimateLength(msgs)
	if n < 20 {
		t.Errorf("expected >= 20, got %d", n)
	}
}

func TestDetectModeInline(t *testing.T) {
	o := New(DefaultConfig())
	msgs := []Message{{Role: "user", Content: "short message"}}
	mode := o.DetectMode(msgs)
	if mode != Inline {
		t.Errorf("expected inline, got %s", mode)
	}
}

func TestDetectModeHybrid(t *testing.T) {
	o := New(Config{HybridThreshold: 100, FileThreshold: 500, MaxRecentMessages: 2})
	msgs := make([]Message, 20)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: strings.Repeat("x", 10)}
	}
	mode := o.DetectMode(msgs)
	if mode != Hybrid {
		t.Errorf("expected hybrid, got %s", mode)
	}
}

func TestDetectModeFile(t *testing.T) {
	o := New(Config{HybridThreshold: 50, FileThreshold: 100, MaxRecentMessages: 2})
	msgs := make([]Message, 20)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: strings.Repeat("x", 10)}
	}
	mode := o.DetectMode(msgs)
	if mode != File {
		t.Errorf("expected file, got %s", mode)
	}
}

func TestProcessInline(t *testing.T) {
	o := New(DefaultConfig())
	msgs := []Message{{Role: "user", Content: "hi"}}
	result := o.Process(msgs)
	if result.Mode != Inline {
		t.Errorf("expected inline, got %s", result.Mode)
	}
	if len(result.InlineMessages) != 1 {
		t.Errorf("expected 1 inline message, got %d", len(result.InlineMessages))
	}
}

func TestProcessHybrid(t *testing.T) {
	o := New(Config{HybridThreshold: 50, FileThreshold: 500, MaxRecentMessages: 2})
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: strings.Repeat("x", 10)}
	}
	result := o.Process(msgs)
	if result.Mode != Hybrid {
		t.Errorf("expected hybrid, got %s", result.Mode)
	}
	if result.FileContent == "" {
		t.Error("expected file content in hybrid mode")
	}
	// Should have system note + recent messages
	if len(result.InlineMessages) != 3 { // 1 system + 2 recent
		t.Errorf("expected 3 inline messages, got %d", len(result.InlineMessages))
	}
}

func TestProcessFile(t *testing.T) {
	o := New(Config{HybridThreshold: 20, FileThreshold: 50, MaxRecentMessages: 1})
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: strings.Repeat("x", 10)}
	}
	result := o.Process(msgs)
	if result.Mode != File {
		t.Errorf("expected file, got %s", result.Mode)
	}
	if result.FileContent == "" {
		t.Error("expected file content")
	}
	// Should have system note + latest user
	if len(result.InlineMessages) != 2 {
		t.Errorf("expected 2 inline messages, got %d", len(result.InlineMessages))
	}
}
