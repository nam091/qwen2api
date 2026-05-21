// Package fewshot injects synthetic few-shot examples into the conversation to
// teach the model how to use MCP and third-party tools. Without these examples
// the model tends to stick with core tools (Read, Write, Bash) and ignore
// namespace-prefixed tools entirely.
package fewshot

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Tool represents a simplified tool definition.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// coreTools that the model already knows well.
var coreToolNames = map[string]struct{}{
	"read": {}, "write": {}, "edit": {}, "multiedit": {},
	"bash": {}, "listdir": {}, "glob": {}, "grep": {},
	"notebookedit": {}, "search": {}, "searchfiles": {},
	"listdirectory": {}, "readfile": {}, "writefile": {},
	"editfile": {}, "runcommand": {}, "runshellcommand": {},
}

// ExtractNamespace extracts the namespace prefix from a tool name.
// E.g. "mcp__playwright__click" -> "mcp__playwright"
func ExtractNamespace(name string) string {
	// Check for double-underscore namespaces: mcp__foo__bar
	parts := strings.SplitN(name, "__", 3)
	if len(parts) >= 3 {
		return parts[0] + "__" + parts[1]
	}
	if len(parts) == 2 {
		return parts[0]
	}
	// Check for single underscore namespace: foo_bar
	idx := strings.LastIndex(name, "_")
	if idx > 0 && idx < len(name)-1 {
		prefix := name[:idx]
		if len(prefix) > 2 && strings.ToLower(prefix) != prefix {
			return prefix
		}
	}
	return ""
}

// IsCoreTool checks if a tool name is a well-known core tool.
func IsCoreTool(name string) bool {
	_, ok := coreToolNames[strings.ToLower(name)]
	return ok
}

// PickRepresentatives selects tools to include in few-shot examples:
// - 1 core tool (Read)
// - Up to maxThirdParty unique namespace representatives
func PickRepresentatives(tools []Tool, maxThirdParty int) []Tool {
	if maxThirdParty <= 0 {
		maxThirdParty = 4
	}
	var result []Tool
	seenNS := make(map[string]bool)

	// Pick one core tool
	for _, t := range tools {
		if IsCoreTool(t.Name) {
			result = append(result, t)
			break
		}
	}

	// Pick one representative per namespace
	for _, t := range tools {
		if IsCoreTool(t.Name) {
			continue
		}
		ns := ExtractNamespace(t.Name)
		if ns == "" {
			ns = t.Name
		}
		if seenNS[ns] {
			continue
		}
		seenNS[ns] = true
		result = append(result, t)
		if len(result)-1 >= maxThirdParty { // -1 for core tool
			break
		}
	}
	return result
}

// FewShotTurn is a single message in a few-shot example.
type FewShotTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// RenderFewShot generates a pair of [user, assistant] messages showing how
// to use the selected tools.
func RenderFewShot(representatives []Tool) []FewShotTurn {
	if len(representatives) == 0 {
		return nil
	}

	var toolUsages []string
	for _, t := range representatives {
		example := generateExample(t)
		toolUsages = append(toolUsages, example)
	}

	userContent := "Show me how to use the available tools effectively."
	assistContent := "I'll demonstrate the available tools:\n\n" + strings.Join(toolUsages, "\n\n")

	return []FewShotTurn{
		{Role: "user", Content: userContent},
		{Role: "assistant", Content: assistContent},
	}
}

func generateExample(t Tool) string {
	params := make(map[string]any)
	if props, ok := t.Parameters["properties"].(map[string]any); ok {
		for k, v := range props {
			params[k] = generateSampleValue(k, v)
		}
	}

	call := map[string]any{
		"name":  t.Name,
		"input": params,
	}
	raw, _ := json.Marshal(call)
	return fmt.Sprintf("<tool_call>\n%s\n</tool_call>", string(raw))
}

func generateSampleValue(key string, schema any) any {
	s, ok := schema.(map[string]any)
	if !ok {
		return "example"
	}
	typ, _ := s["type"].(string)
	switch typ {
	case "string":
		return sampleString(key)
	case "number", "integer":
		return 1
	case "boolean":
		return true
	case "array":
		return []any{sampleString(key)}
	default:
		return "example"
	}
}

func sampleString(key string) string {
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "path") || strings.Contains(lower, "file"):
		return "/path/to/file.txt"
	case strings.Contains(lower, "url"):
		return "https://example.com"
	case strings.Contains(lower, "command") || strings.Contains(lower, "cmd"):
		return "ls -la"
	case strings.Contains(lower, "query") || strings.Contains(lower, "search"):
		return "search query"
	case strings.Contains(lower, "content") || strings.Contains(lower, "text"):
		return "content here"
	default:
		return "example_value"
	}
}

// ToolSummary returns a concise one-line summary of selected tools for logging.
func ToolSummary(tools []Tool) string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}
