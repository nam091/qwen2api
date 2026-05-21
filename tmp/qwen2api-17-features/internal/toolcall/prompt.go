package toolcall

import (
	"fmt"
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/schemacompressor"
	"github.com/keaume34/qwen2api/internal/toolname"
)

// FormatToolsPrompt generates a system prompt section describing available tools
// and the expected output format for the model. Tool names are obfuscated to
// avoid Qwen internal validation rejection.
func FormatToolsPrompt(tools []openai.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("You have access to the following tools. To call a tool, output a <tool_call> block.\n\n")
	b.WriteString("Available tools:\n\n")

	for _, tool := range tools {
		obfuscatedName := toolname.ToQwen(tool.Function.Name)
		sig := schemacompressor.CompressToolSchema(obfuscatedName, tool.Function.Parameters)
		if tool.Function.Description != "" {
			b.WriteString(fmt.Sprintf("<tool>%s // %s</tool>\n\n", sig, tool.Function.Description))
			continue
		}
		b.WriteString(fmt.Sprintf("<tool>%s</tool>\n\n", sig))
	}

	b.WriteString("Use the exact obfuscated tool name shown above in the tool_call JSON.\n")

	b.WriteString("When you want to call a tool, use this exact format:\n")
	b.WriteString("<tool_call>\n")
	b.WriteString("{\"name\": \"tool_name\", \"arguments\": {\"param1\": \"value1\"}}\n")
	b.WriteString("</tool_call>\n\n")
	b.WriteString("You may call multiple tools by using multiple <tool_call> blocks.\n")
	b.WriteString("If you don't need to call any tools, respond normally without <tool_call> blocks.")

	return b.String()
}
