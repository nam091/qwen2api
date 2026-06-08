package server

import (
	"encoding/json"
	"testing"

	"github.com/keaume34/qwen2api/internal/openai"
)

func TestClaudeCodeOptimizer_ExtractNewContent_SingleMessage(t *testing.T) {
	optimizer := NewClaudeCodeOptimizer(true)

	msgs := []openai.ChatMessage{
		{
			Role:    "user",
			Content: json.RawMessage(`"Hello, how are you?"`),
		},
	}

	result := optimizer.ExtractNewContent(msgs)
	expected := "Hello, how are you?"

	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

func TestClaudeCodeOptimizer_ExtractNewContent_ToolOutput(t *testing.T) {
	optimizer := NewClaudeCodeOptimizer(true)

	msgs := []openai.ChatMessage{
		{
			Role:    "user",
			Content: json.RawMessage(`"Read the file"`),
		},
		{
			Role:    "assistant",
			Content: json.RawMessage(`"I'll read the file for you"`),
		},
		{
			Role:    "tool",
			Content: json.RawMessage(`"file content here"`),
		},
	}

	result := optimizer.ExtractNewContent(msgs)
	expected := "file content here"

	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

func TestClaudeCodeOptimizer_ExtractNewContent_AssistantWithToolCalls(t *testing.T) {
	optimizer := NewClaudeCodeOptimizer(true)

	msgs := []openai.ChatMessage{
		{
			Role:    "user",
			Content: json.RawMessage(`"Read the file"`),
		},
		{
			Role:    "assistant",
			Content: json.RawMessage(`"I'll read the file"`),
			ToolCalls: []openai.ToolCall{
				{
					ID:   "call_123",
					Type: "function",
					Function: openai.ToolCallFunction{
						Name:      "Read",
						Arguments: `{"file_path": "/path/to/file"}`,
					},
				},
			},
		},
	}

	result := optimizer.ExtractNewContent(msgs)

	if result == "" {
		t.Error("expected non-empty result")
	}

	// Should contain tool_call format
	if !contains(result, "<tool_call>") {
		t.Error("expected result to contain <tool_call>")
	}
	if !contains(result, "Read") {
		t.Error("expected result to contain tool name 'Read'")
	}
}

func TestClaudeCodeOptimizer_ExtractNewContent_LastUserMessage(t *testing.T) {
	optimizer := NewClaudeCodeOptimizer(true)

	msgs := []openai.ChatMessage{
		{
			Role:    "user",
			Content: json.RawMessage(`"First message"`),
		},
		{
			Role:    "assistant",
			Content: json.RawMessage(`"Response to first"`),
		},
		{
			Role:    "user",
			Content: json.RawMessage(`"Second message"`),
		},
	}

	result := optimizer.ExtractNewContent(msgs)
	expected := "Second message"

	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

func TestClaudeCodeOptimizer_ExtractNewContent_Disabled(t *testing.T) {
	optimizer := NewClaudeCodeOptimizer(false)

	msgs := []openai.ChatMessage{
		{
			Role:    "user",
			Content: json.RawMessage(`"Hello"`),
		},
		{
			Role:    "assistant",
			Content: json.RawMessage(`"Hi there"`),
		},
		{
			Role:    "user",
			Content: json.RawMessage(`"How are you?"`),
		},
	}

	result := optimizer.ExtractNewContent(msgs)

	// When disabled, should collapse all messages
	if result == "" {
		t.Error("expected non-empty result")
	}

	// Should contain multiple messages
	if !contains(result, "Hello") || !contains(result, "How are you?") {
		t.Error("expected result to contain both messages when disabled")
	}
}

func TestClaudeCodeOptimizer_ShouldOptimize(t *testing.T) {
	optimizer := NewClaudeCodeOptimizer(true)

	// Should optimize for Claude Code with conversation_id
	if !optimizer.ShouldOptimize(ClientClaudeCode, "conv-123") {
		t.Error("expected ShouldOptimize to return true for Claude Code with conversation_id")
	}

	// Should not optimize for Claude Code without conversation_id
	if optimizer.ShouldOptimize(ClientClaudeCode, "") {
		t.Error("expected ShouldOptimize to return false for Claude Code without conversation_id")
	}

	// Should not optimize for other clients
	if optimizer.ShouldOptimize(ClientUnknown, "conv-123") {
		t.Error("expected ShouldOptimize to return false for unknown client")
	}

	// Should not optimize when disabled
	optimizerDisabled := NewClaudeCodeOptimizer(false)
	if optimizerDisabled.ShouldOptimize(ClientClaudeCode, "conv-123") {
		t.Error("expected ShouldOptimize to return false when disabled")
	}
}

func TestClaudeCodeOptimizer_GetOptimizationStats(t *testing.T) {
	optimizer := NewClaudeCodeOptimizer(true)

	original := "This is a long message with many tokens"
	optimized := "Short"

	stats := optimizer.GetOptimizationStats(original, optimized)

	if stats["original_tokens"].(int) <= 0 {
		t.Error("expected original_tokens to be positive")
	}
	if stats["optimized_tokens"].(int) <= 0 {
		t.Error("expected optimized_tokens to be positive")
	}
	if stats["saved_tokens"].(int) <= 0 {
		t.Error("expected saved_tokens to be positive")
	}
	if stats["saved_percent"].(float64) <= 0 {
		t.Error("expected saved_percent to be positive")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}