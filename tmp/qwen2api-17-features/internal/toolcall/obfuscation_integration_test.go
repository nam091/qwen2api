package toolcall

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/keaume34/qwen2api/internal/openai"
)

func TestObfuscation_EndToEnd(t *testing.T) {
	// Simulate client sending tools with original names
	tools := []openai.Tool{
		{
			Type: "function",
			Function: openai.ToolFunction{
				Name:        "Read",
				Description: "Read a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		},
		{
			Type: "function",
			Function: openai.ToolFunction{
				Name:        "Write",
				Description: "Write a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`),
			},
		},
		{
			Type: "function",
			Function: openai.ToolFunction{
				Name:        "CustomTool",
				Description: "Custom tool",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			},
		},
	}

	// Generate prompt with obfuscated names
	prompt := FormatToolsPrompt(tools)

	// Verify obfuscated names appear in prompt
	if !strings.Contains(prompt, "fs_open_file") {
		t.Error("Prompt should contain obfuscated name 'fs_open_file' for Read")
	}
	if !strings.Contains(prompt, "fs_put_file") {
		t.Error("Prompt should contain obfuscated name 'fs_put_file' for Write")
	}
	if !strings.Contains(prompt, "u_CustomTool") {
		t.Error("Prompt should contain obfuscated name 'u_CustomTool' for CustomTool")
	}

	// Verify original names do NOT appear in prompt
	if strings.Contains(prompt, `"name": "Read"`) {
		t.Error("Prompt should NOT contain original name 'Read'")
	}
	if strings.Contains(prompt, `"name": "Write"`) {
		t.Error("Prompt should NOT contain original name 'Write'")
	}
}

func TestObfuscation_ParseResponse(t *testing.T) {
	// Simulate Qwen returning obfuscated tool names
	tests := []struct {
		name           string
		response       string
		expectedName   string
		expectedArgKey string
	}{
		{
			name:           "Obfuscated Read",
			response:       `<tool_call>{"name": "fs_open_file", "arguments": {"path": "/foo"}}</tool_call>`,
			expectedName:   "Read",
			expectedArgKey: "path",
		},
		{
			name:           "Obfuscated Write",
			response:       `<tool_call>{"name": "fs_put_file", "arguments": {"path": "/bar", "content": "test"}}</tool_call>`,
			expectedName:   "Write",
			expectedArgKey: "path",
		},
		{
			name:           "Prefixed custom tool",
			response:       `<tool_call>{"name": "u_CustomTool", "arguments": {"param": "value"}}</tool_call>`,
			expectedName:   "CustomTool",
			expectedArgKey: "param",
		},
		{
			name:           "XML format obfuscated",
			response:       `<tool_call><function=fs_open_file><parameter=path>/test</parameter></function></tool_call>`,
			expectedName:   "Read",
			expectedArgKey: "path",
		},
		{
			name:           "Claude-style obfuscated",
			response:       `<function_calls><invoke name="fs_put_file"><parameter name="path">/x</parameter></invoke></function_calls>`,
			expectedName:   "Write",
			expectedArgKey: "path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Parse(tt.response)
			if len(result.ToolCalls) != 1 {
				t.Fatalf("Expected 1 tool call, got %d", len(result.ToolCalls))
			}

			tc := result.ToolCalls[0]
			if tc.Function.Name != tt.expectedName {
				t.Errorf("Expected de-obfuscated name %q, got %q", tt.expectedName, tc.Function.Name)
			}

			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				t.Fatalf("Failed to parse arguments: %v", err)
			}

			if _, ok := args[tt.expectedArgKey]; !ok {
				t.Errorf("Expected argument key %q not found in %v", tt.expectedArgKey, args)
			}
		})
	}
}
