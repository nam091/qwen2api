package toolcall

import (
	"fmt"
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/schemacompressor"
)

// FormatToolsPrompt generates a system prompt section describing available tools
// and the expected output format for the model.
func FormatToolsPrompt(tools []openai.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Available Tools\n\n")
	b.WriteString("You have access to the following tools. When you need to use a tool, you MUST output a <tool_call> XML block with the exact tool name shown below.\n\n")

	// Use compact format when there are many tools to reduce prompt size
	compact := len(tools) > 15

	for _, tool := range tools {
		// Use original client name in the prompt — the model should call tools
		// by the name the client defined. Obfuscation only matters for the
		// upstream Qwen API payload, not for instructions to the model.
		// The parser's FromQwen() handles deobfuscation on the way back.
		toolName := tool.Function.Name
		sig := schemacompressor.CompressToolSchema(toolName, tool.Function.Parameters)
		if tool.Function.Description != "" {
			desc := tool.Function.Description
			if compact {
				desc = truncateDescription(desc)
			}
			b.WriteString(fmt.Sprintf("## Tool: %s\n%s\n\nSignature: `%s`\n\n", toolName, desc, sig))
			continue
		}
		b.WriteString(fmt.Sprintf("## Tool: %s\nSignature: `%s`\n\n", toolName, sig))
	}

	b.WriteString("## How to call a tool\n\n")
	b.WriteString("When you want to call a tool, output a <tool_call> block with JSON inside. Example:\n\n")
	b.WriteString("<tool_call>\n")
	b.WriteString("{\"name\": \"tool_name\", \"arguments\": {\"param1\": \"value1\"}}\n")
	b.WriteString("</tool_call>\n\n")
	b.WriteString("IMPORTANT:\n")
	b.WriteString("- Use the EXACT tool name from the list above (case-sensitive)\n")
	b.WriteString("- You may call multiple tools by using multiple <tool_call> blocks\n")
	b.WriteString("- If you don't need any tools, respond normally without <tool_call> blocks\n")
	b.WriteString("- Always use valid JSON for arguments")

	return b.String()
}

// truncateDescription shortens a tool description to the first sentence
// or first 120 characters, whichever is shorter. This significantly reduces
// prompt size when many tools are present.
func truncateDescription(desc string) string {
	desc = strings.TrimSpace(desc)
	if len(desc) <= 120 {
		return desc
	}
	// Try to cut at first sentence boundary
	for i, r := range desc {
		if i > 0 && (r == '.' || r == '!') {
			next := i + 1
			if next < len(desc) && desc[next] == ' ' {
				return desc[:next+1]
			}
		}
		if i >= 120 {
			break
		}
	}
	// Hard cut at 120 chars
	if len(desc) > 120 {
		return desc[:120] + "..."
	}
	return desc
}
