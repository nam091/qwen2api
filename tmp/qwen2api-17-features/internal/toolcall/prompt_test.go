package toolcall

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/keaume34/qwen2api/internal/openai"
)

func TestFormatToolsPrompt_UsesObfuscatedCompressedSchema(t *testing.T) {
	tools := []openai.Tool{
		{
			Type: "function",
			Function: openai.ToolFunction{
				Name:        "Read",
				Description: "read file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: openai.ToolFunction{
				Name:       "CustomTool",
				Parameters: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			},
		},
	}

	prompt := FormatToolsPrompt(tools)

	if !strings.Contains(prompt, "fs_open_file({path: string})") {
		t.Fatalf("expected compressed obfuscated Read signature, got: %s", prompt)
	}
	if !strings.Contains(prompt, "u_CustomTool({q?: string})") {
		t.Fatalf("expected compressed prefixed CustomTool signature, got: %s", prompt)
	}
	if strings.Contains(prompt, `"type":"object"`) {
		t.Fatalf("prompt should not include raw JSON schema after compression: %s", prompt)
	}
}
