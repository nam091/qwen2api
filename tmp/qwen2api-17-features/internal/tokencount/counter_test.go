package tokencount

import "testing"

func TestCountTokens(t *testing.T) {
	c := New(4.0)
	n := c.CountTokens("Hello, world!")
	if n < 2 || n > 10 {
		t.Errorf("unexpected token count %d", n)
	}
}

func TestCountTokensCJK(t *testing.T) {
	c := New(4.0)
	n := c.CountTokens("你好世界")
	// 4 CJK chars × ~2 tokens = ~8
	if n < 6 || n > 10 {
		t.Errorf("unexpected CJK token count %d", n)
	}
}

func TestCountMessages(t *testing.T) {
	c := New(4.0)
	msgs := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello"},
	}
	n := c.CountMessages(msgs)
	if n < 10 {
		t.Errorf("expected at least 10 tokens, got %d", n)
	}
}

func TestNewUsage(t *testing.T) {
	c := New(4.0)
	msgs := []Message{{Role: "user", Content: "hi"}}
	u := c.NewUsage(msgs, "hello there")
	if u.TotalTokens != u.PromptTokens+u.CompletionTokens {
		t.Error("total should equal prompt + completion")
	}
	if u.PromptTokens < 1 || u.CompletionTokens < 1 {
		t.Error("both should be >= 1")
	}
}

func TestEstimatePromptLength(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hello world"}}
	n := EstimatePromptLength(msgs)
	if n < 15 {
		t.Errorf("expected >= 15, got %d", n)
	}
}
