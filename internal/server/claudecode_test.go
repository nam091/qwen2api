package server

import (
	"encoding/json"
	"testing"

	"github.com/keaume34/qwen2api/internal/database"
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

	// Should optimize for Claude Code without conversation_id
	if !optimizer.ShouldOptimize(ClientClaudeCode, "") {
		t.Error("expected ShouldOptimize to return true for Claude Code without conversation_id")
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

func TestGenerateConversationID(t *testing.T) {
	// Same inputs should produce same ID (Claude Code)
	id1 := GenerateConversationID("claude-code/1.0", "Hello", "192.168.1.1", true)
	id2 := GenerateConversationID("claude-code/1.0", "Hello", "192.168.1.1", true)
	if id1 != id2 {
		t.Errorf("expected same ID for same inputs, got %s and %s", id1, id2)
	}

	// Different IP should produce different ID (Claude Code)
	id3 := GenerateConversationID("claude-code/1.0", "Hello", "192.168.1.2", true)
	if id1 == id3 {
		t.Error("expected different ID for different IP")
	}

	// Same IP, different message should produce same ID (Claude Code - session based)
	id4 := GenerateConversationID("claude-code/1.0", "World", "192.168.1.1", true)
	if id1 != id4 {
		t.Error("expected same ID for same IP (Claude Code session)")
	}

	// Non-Claude Code: different message should produce different ID
	id5 := GenerateConversationID("chrome/1.0", "Hello", "192.168.1.1", false)
	id6 := GenerateConversationID("chrome/1.0", "World", "192.168.1.1", false)
	if id5 == id6 {
		t.Error("expected different ID for different messages (non-Claude Code)")
	}

	// ID should be 16 chars
	if len(id1) != 16 {
		t.Errorf("expected ID length 16, got %d", len(id1))
	}
}

func TestFindNewMessages_NoStoredMessages(t *testing.T) {
	requestMessages := []openai.ChatMessage{
		{Role: "user", Content: json.RawMessage(`"Hello"`)},
		{Role: "assistant", Content: json.RawMessage(`"Hi"`)},
	}

	result := FindNewMessages(requestMessages, nil)
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}

func TestFindNewMessages_WithStoredMessages(t *testing.T) {
	requestMessages := []openai.ChatMessage{
		{Role: "user", Content: json.RawMessage(`"Hello"`)},
		{Role: "assistant", Content: json.RawMessage(`"Hi"`)},
		{Role: "user", Content: json.RawMessage(`"How are you?"`)},
	}

	storedMessages := []*database.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}

	result := FindNewMessages(requestMessages, storedMessages)
	if len(result) != 1 {
		t.Errorf("expected 1 new message, got %d", len(result))
	}
	if result[0].Text() != "How are you?" {
		t.Errorf("expected 'How are you?', got '%s'", result[0].Text())
	}
}

func TestFindNewMessages_ToolOutput(t *testing.T) {
	requestMessages := []openai.ChatMessage{
		{Role: "user", Content: json.RawMessage(`"Read file"`)},
		{Role: "assistant", Content: json.RawMessage(`"Reading..."`)},
		{Role: "tool", Content: json.RawMessage(`"file content"`)},
	}

	storedMessages := []*database.Message{
		{Role: "user", Content: "Read file"},
		{Role: "assistant", Content: "Reading..."},
	}

	result := FindNewMessages(requestMessages, storedMessages)
	if len(result) != 1 {
		t.Errorf("expected 1 new message, got %d", len(result))
	}
	if result[0].Role != "tool" {
		t.Errorf("expected tool role, got %s", result[0].Role)
	}
}