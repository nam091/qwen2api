package hallucination

import (
	"testing"

	"github.com/keaume34/qwen2api/internal/openai"
)

func TestDetectRefusal(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"I cannot access that file", true},
		{"I'm unable to perform this action", true},
		{"I can't help with that", true},
		{"Sorry, I cannot do that", true},
		{"auto_agent_blocked", true},
		{"Tool does not exist", true},
		{"Here is the result", false},
		{"Successfully completed", false},
		{"", false},
	}

	for _, tt := range tests {
		got := DetectRefusal(tt.text)
		if got != tt.want {
			t.Errorf("DetectRefusal(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestDetectBlocked(t *testing.T) {
	tests := []struct {
		text    string
		blocked bool
		reason  string
	}{
		{"auto_agent_blocked", true, "auto_agent_blocked"},
		{"Tool does not exist", true, "tool_not_found"},
		{"Tool X does not exist", true, "tool_not_found"},
		{"Normal response", false, ""},
		{"", false, ""},
	}

	for _, tt := range tests {
		got := DetectBlocked(tt.text)
		if got.Blocked != tt.blocked {
			t.Errorf("DetectBlocked(%q).Blocked = %v, want %v", tt.text, got.Blocked, tt.blocked)
		}
		if got.Blocked && got.Reason != tt.reason {
			t.Errorf("DetectBlocked(%q).Reason = %q, want %q", tt.text, got.Reason, tt.reason)
		}
	}
}

func TestDetectDuplicates(t *testing.T) {
	calls := []openai.ToolCall{
		{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/foo"}`}},
		{Function: openai.ToolCallFunction{Name: "Write", Arguments: `{"path":"/bar"}`}},
		{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/foo"}`}}, // Duplicate
		{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/baz"}`}}, // Different args
		{Function: openai.ToolCallFunction{Name: "Write", Arguments: `{"path":"/bar"}`}}, // Duplicate
	}

	duplicates := DetectDuplicates(calls)
	if len(duplicates) != 2 {
		t.Errorf("Expected 2 duplicates, got %d: %v", len(duplicates), duplicates)
	}

	// Should be indices 2 and 4
	expected := map[int]bool{2: true, 4: true}
	for _, idx := range duplicates {
		if !expected[idx] {
			t.Errorf("Unexpected duplicate index: %d", idx)
		}
	}
}

func TestFilterDuplicates(t *testing.T) {
	calls := []openai.ToolCall{
		{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/foo"}`}},
		{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/foo"}`}}, // Duplicate
		{Function: openai.ToolCallFunction{Name: "Write", Arguments: `{"path":"/bar"}`}},
	}

	filtered := FilterDuplicates(calls)
	if len(filtered) != 2 {
		t.Errorf("Expected 2 calls after filtering, got %d", len(filtered))
	}

	// Should keep first Read and Write
	if filtered[0].Function.Name != "Read" {
		t.Errorf("First call should be Read, got %s", filtered[0].Function.Name)
	}
	if filtered[1].Function.Name != "Write" {
		t.Errorf("Second call should be Write, got %s", filtered[1].Function.Name)
	}
}

func TestValidateToolCall(t *testing.T) {
	tests := []struct {
		name string
		call openai.ToolCall
		want bool
	}{
		{
			name: "Valid call",
			call: openai.ToolCall{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/foo"}`}},
			want: true,
		},
		{
			name: "Empty name",
			call: openai.ToolCall{Function: openai.ToolCallFunction{Name: "", Arguments: `{"path":"/foo"}`}},
			want: false,
		},
		{
			name: "Empty arguments",
			call: openai.ToolCall{Function: openai.ToolCallFunction{Name: "Read", Arguments: ""}},
			want: false,
		},
		{
			name: "Null arguments",
			call: openai.ToolCall{Function: openai.ToolCallFunction{Name: "Read", Arguments: "null"}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateToolCall(tt.call)
			if got != tt.want {
				t.Errorf("ValidateToolCall() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterInvalid(t *testing.T) {
	calls := []openai.ToolCall{
		{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/foo"}`}},
		{Function: openai.ToolCallFunction{Name: "", Arguments: `{"path":"/bar"}`}},      // Invalid: empty name
		{Function: openai.ToolCallFunction{Name: "Write", Arguments: ""}},                // Invalid: empty args
		{Function: openai.ToolCallFunction{Name: "Edit", Arguments: "null"}},             // Invalid: null args
		{Function: openai.ToolCallFunction{Name: "Grep", Arguments: `{"pattern":"test"}`}},
	}

	filtered := FilterInvalid(calls)
	if len(filtered) != 2 {
		t.Errorf("Expected 2 valid calls, got %d", len(filtered))
	}

	if filtered[0].Function.Name != "Read" {
		t.Errorf("First valid call should be Read, got %s", filtered[0].Function.Name)
	}
	if filtered[1].Function.Name != "Grep" {
		t.Errorf("Second valid call should be Grep, got %s", filtered[1].Function.Name)
	}
}

func TestSanitize(t *testing.T) {
	calls := []openai.ToolCall{
		{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/foo"}`}},
		{Function: openai.ToolCallFunction{Name: "", Arguments: `{"path":"/bar"}`}},       // Invalid
		{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/foo"}`}},   // Duplicate
		{Function: openai.ToolCallFunction{Name: "Write", Arguments: `{"path":"/baz"}`}},
	}

	sanitized, filtered := Sanitize(calls)
	if !filtered {
		t.Error("Expected filtered=true")
	}
	if len(sanitized) != 2 {
		t.Errorf("Expected 2 sanitized calls, got %d", len(sanitized))
	}

	// Should have Read and Write
	if sanitized[0].Function.Name != "Read" {
		t.Errorf("First call should be Read, got %s", sanitized[0].Function.Name)
	}
	if sanitized[1].Function.Name != "Write" {
		t.Errorf("Second call should be Write, got %s", sanitized[1].Function.Name)
	}
}

func TestSanitize_NoFiltering(t *testing.T) {
	calls := []openai.ToolCall{
		{Function: openai.ToolCallFunction{Name: "Read", Arguments: `{"path":"/foo"}`}},
		{Function: openai.ToolCallFunction{Name: "Write", Arguments: `{"path":"/bar"}`}},
	}

	sanitized, filtered := Sanitize(calls)
	if filtered {
		t.Error("Expected filtered=false for valid calls")
	}
	if len(sanitized) != 2 {
		t.Errorf("Expected 2 calls, got %d", len(sanitized))
	}
}
