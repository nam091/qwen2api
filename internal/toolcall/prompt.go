package toolcall

import (
	"encoding/json"
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
)

// FormatToolsPrompt generates a system prompt section describing available tools
// and the expected output format for the model.
func FormatToolsPrompt(tools []openai.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("You have access to the following tools. To call a tool, output a <tool_call> block.\n\n")
	b.WriteString("Available tools:\n\n")

	for _, tool := range tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			continue
		}
		b.WriteString("<tool>\n")
		b.Write(raw)
		b.WriteString("\n</tool>\n\n")
	}

	b.WriteString("When you want to call a tool, use this exact format:\n")
	b.WriteString("<tool_call>\n")
	b.WriteString("{\"name\": \"tool_name\", \"arguments\": {\"param1\": \"value1\"}}\n")
	b.WriteString("</tool_call>\n\n")
	b.WriteString("You may call multiple tools by using multiple <tool_call> blocks.\n")
	b.WriteString("If you don't need to call any tools, respond normally without <tool_call> blocks.")

	return b.String()
}
