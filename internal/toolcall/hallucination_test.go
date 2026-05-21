package toolcall

import (
	"testing"
)

func TestParse_HallucinationProtection_Duplicates(t *testing.T) {
	// Response with duplicate tool calls
	input := `<tool_call>
{"name": "Read", "arguments": {"path": "/foo"}}
</tool_call>
<tool_call>
{"name": "Read", "arguments": {"path": "/foo"}}
</tool_call>
<tool_call>
{"name": "Write", "arguments": {"path": "/bar"}}
</tool_call>`

	result := Parse(input)
	// Should filter out duplicate Read call
	if len(result.ToolCalls) != 2 {
		t.Errorf("Expected 2 calls after deduplication, got %d", len(result.ToolCalls))
	}

	if result.ToolCalls[0].Function.Name != "Read" {
		t.Errorf("First call should be Read, got %s", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "Write" {
		t.Errorf("Second call should be Write, got %s", result.ToolCalls[1].Function.Name)
	}
}

func TestParse_HallucinationProtection_Invalid(t *testing.T) {
	// Response with invalid tool calls
	input := `<tool_call>
{"name": "Read", "arguments": {"path": "/foo"}}
</tool_call>
<tool_call>
{"name": "", "arguments": {"path": "/bar"}}
</tool_call>
<tool_call>
{"name": "Write", "arguments": null}
</tool_call>
<tool_call>
{"name": "Grep", "arguments": {"pattern": "test"}}
</tool_call>`

	result := Parse(input)
	// Should filter out invalid calls (empty name, null args)
	if len(result.ToolCalls) != 2 {
		t.Errorf("Expected 2 valid calls, got %d", len(result.ToolCalls))
	}

	if result.ToolCalls[0].Function.Name != "Read" {
		t.Errorf("First call should be Read, got %s", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "Grep" {
		t.Errorf("Second call should be Grep, got %s", result.ToolCalls[1].Function.Name)
	}
}

func TestParse_HallucinationProtection_Combined(t *testing.T) {
	// Response with both invalid and duplicate calls
	input := `<tool_call>
{"name": "Read", "arguments": {"path": "/foo"}}
</tool_call>
<tool_call>
{"name": "", "arguments": {"path": "/bar"}}
</tool_call>
<tool_call>
{"name": "Read", "arguments": {"path": "/foo"}}
</tool_call>
<tool_call>
{"name": "Write", "arguments": {"path": "/baz"}}
</tool_call>`

	result := Parse(input)
	// Should filter out invalid (empty name) and duplicate Read
	if len(result.ToolCalls) != 2 {
		t.Errorf("Expected 2 calls after sanitization, got %d", len(result.ToolCalls))
	}

	if result.ToolCalls[0].Function.Name != "Read" {
		t.Errorf("First call should be Read, got %s", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "Write" {
		t.Errorf("Second call should be Write, got %s", result.ToolCalls[1].Function.Name)
	}
}

func TestParse_HallucinationProtection_AllValid(t *testing.T) {
	// Response with all valid, unique calls
	input := `<tool_call>
{"name": "Read", "arguments": {"path": "/foo"}}
</tool_call>
<tool_call>
{"name": "Write", "arguments": {"path": "/bar"}}
</tool_call>`

	result := Parse(input)
	// Should keep all calls
	if len(result.ToolCalls) != 2 {
		t.Errorf("Expected 2 calls, got %d", len(result.ToolCalls))
	}
}
