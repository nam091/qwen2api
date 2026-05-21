package toolcall

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/keaume34/qwen2api/internal/hallucination"
	"github.com/keaume34/qwen2api/internal/jsonrepair"
	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/toolname"
)

var (
	reToolCallBlock     = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*</tool_call>`)
	reFunctionCallBlock = regexp.MustCompile(`(?s)<function_calls>\s*(.*?)\s*</function_calls>`)
	reInvokeBlock       = regexp.MustCompile(`(?s)<invoke[^>]*name="([^"]+)"[^>]*>(.*?)</invoke>`)
	reInvokeParameter   = regexp.MustCompile(`(?s)<parameter[^>]*name="([^"]+)"[^>]*>(.*?)</parameter>`)
	reFunctionXML       = regexp.MustCompile(`(?s)<function=([^>]+)>(.*?)</function>`)
	reParameter         = regexp.MustCompile(`(?s)<parameter=([^>]+)>(.*?)</parameter>`)
	reBareJSON          = regexp.MustCompile(`(?s)\{\s*"name"\s*:\s*"[^"]+"\s*,\s*"arguments"\s*:\s*(?:\{.*?\}|"[^"]*"|null)\s*\}`)
)

// ParseResult holds the outcome of parsing response text for tool calls.
type ParseResult struct {
	Content   string
	ToolCalls []openai.ToolCall
}

// Parse extracts all <tool_call>...</tool_call> blocks from text.
// Returns the remaining content and structured tool calls.
func Parse(text string) ParseResult {
	return ParseWithFormats(text, true)
}

// ParseWithFormats extracts tool calls from text. When multiFormat is true,
// also recognizes Claude-style <function_calls><invoke> blocks and bare JSON.
// Applies hallucination protection to filter invalid/duplicate calls.
func ParseWithFormats(text string, multiFormat bool) ParseResult {
	calls, content := parseToolCallBlocks(text)
	if !multiFormat {
		// Apply hallucination protection
		calls, _ = hallucination.Sanitize(calls)
		return ParseResult{Content: strings.TrimSpace(content), ToolCalls: calls}
	}

	// Claude-style <function_calls> blocks.
	moreCalls, content := parseFunctionCallBlocks(content)
	calls = append(calls, moreCalls...)

	// Bare JSON tool calls (no <tool_call> wrapper).
	moreCalls, content = parseBareJSON(content)
	calls = append(calls, moreCalls...)

	// Apply hallucination protection: remove invalid/duplicate calls
	calls, _ = hallucination.Sanitize(calls)

	return ParseResult{
		Content:   strings.TrimSpace(content),
		ToolCalls: calls,
	}
}

func parseToolCallBlocks(text string) ([]openai.ToolCall, string) {
	matches := reToolCallBlock.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil, text
	}
	var calls []openai.ToolCall
	var content strings.Builder
	lastEnd := 0
	for _, loc := range matches {
		content.WriteString(text[lastEnd:loc[0]])
		lastEnd = loc[1]
		inner := strings.TrimSpace(text[loc[2]:loc[3]])
		if tc, ok := parseBlock(inner); ok {
			calls = append(calls, tc)
		} else {
			content.WriteString(text[loc[0]:loc[1]])
		}
	}
	content.WriteString(text[lastEnd:])
	return calls, content.String()
}

func parseFunctionCallBlocks(text string) ([]openai.ToolCall, string) {
	matches := reFunctionCallBlock.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil, text
	}
	var calls []openai.ToolCall
	var content strings.Builder
	lastEnd := 0
	for _, loc := range matches {
		content.WriteString(text[lastEnd:loc[0]])
		lastEnd = loc[1]
		inner := strings.TrimSpace(text[loc[2]:loc[3]])
		invokes := reInvokeBlock.FindAllStringSubmatch(inner, -1)
		for _, inv := range invokes {
			name := strings.TrimSpace(inv[1])
			body := inv[2]

			// De-obfuscate tool name
			clientName := toolname.FromQwen(name)

			params := map[string]string{}
			for _, pm := range reInvokeParameter.FindAllStringSubmatch(body, -1) {
				params[strings.TrimSpace(pm[1])] = strings.TrimSpace(pm[2])
			}
			argsJSON, err := json.Marshal(params)
			if err != nil {
				continue
			}
			calls = append(calls, openai.ToolCall{
				ID:   generateID(),
				Type: "function",
				Function: openai.ToolCallFunction{
					Name:      clientName,
					Arguments: string(argsJSON),
				},
			})
		}
	}
	content.WriteString(text[lastEnd:])
	return calls, content.String()
}

func parseBareJSON(text string) ([]openai.ToolCall, string) {
	matches := reBareJSON.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil, text
	}
	var calls []openai.ToolCall
	var content strings.Builder
	lastEnd := 0
	for _, loc := range matches {
		content.WriteString(text[lastEnd:loc[0]])
		lastEnd = loc[1]
		candidate := text[loc[0]:loc[1]]
		if tc, ok := parseJSON(candidate); ok {
			calls = append(calls, tc)
		} else {
			content.WriteString(candidate)
		}
	}
	content.WriteString(text[lastEnd:])
	return calls, content.String()
}

func parseBlock(inner string) (openai.ToolCall, bool) {
	// Try to repair the entire block first if it's not valid JSON
	if !json.Valid([]byte(inner)) {
		repaired, changed := jsonrepair.Repair(inner)
		if changed {
			inner = repaired
		}
	}

	if tc, ok := parseJSON(inner); ok {
		return tc, true
	}
	if tc, ok := parseXML(inner); ok {
		return tc, true
	}
	return openai.ToolCall{}, false
}

func parseJSON(inner string) (openai.ToolCall, bool) {
	var raw struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(inner), &raw); err != nil || raw.Name == "" {
		return openai.ToolCall{}, false
	}

	// De-obfuscate tool name from Qwen back to client name
	clientName := toolname.FromQwen(raw.Name)

	var argsStr string
	if len(raw.Arguments) > 0 {
		argsStr = string(raw.Arguments)
		// Validate and repair arguments JSON if needed
		if !json.Valid([]byte(argsStr)) {
			repaired, _ := jsonrepair.Repair(argsStr)
			argsStr = repaired
		}
	} else {
		argsStr = "{}"
	}

	return openai.ToolCall{
		ID:   generateID(),
		Type: "function",
		Function: openai.ToolCallFunction{
			Name:      clientName,
			Arguments: argsStr,
		},
	}, true
}

func parseXML(inner string) (openai.ToolCall, bool) {
	funcMatch := reFunctionXML.FindStringSubmatch(inner)
	if funcMatch == nil {
		return openai.ToolCall{}, false
	}

	funcName := strings.TrimSpace(funcMatch[1])
	funcBody := funcMatch[2]

	// De-obfuscate tool name
	clientName := toolname.FromQwen(funcName)

	params := map[string]string{}
	paramMatches := reParameter.FindAllStringSubmatch(funcBody, -1)
	for _, pm := range paramMatches {
		key := strings.TrimSpace(pm[1])
		value := strings.TrimSpace(pm[2])
		params[key] = value
	}

	argsJSON, err := json.Marshal(params)
	if err != nil {
		return openai.ToolCall{}, false
	}

	return openai.ToolCall{
		ID:   generateID(),
		Type: "function",
		Function: openai.ToolCallFunction{
			Name:      clientName,
			Arguments: string(argsJSON),
		},
	}, true
}

func generateID() string {
	return fmt.Sprintf("call_%s", uuid.NewString()[:8])
}
