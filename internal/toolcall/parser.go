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
	reParameter         = regexp.MustCompile(`(?s)<parameter=([^>]+?)>(.*?)</parameter>`)
	reBareJSON          = regexp.MustCompile(`(?s)\{\s*"name"\s*:\s*"[^"]+"\s*,\s*"arguments"\s*:\s*(?:\{.*?\}|"[^"]*"|null)\s*\}`)
	// reWrappedFence matches when the entire content is wrapped in a code fence
	// (```json ... ```) to avoid touching fences that appear legitimately
	// inside argument values (e.g. code snippets in string fields).
	reWrappedFence = regexp.MustCompile("(?s)^```[a-zA-Z0-9]*\\s*(.*?)\\s*```$")
	// reHybridToolCallBlock matches <tool_call> openers paired with non-
	// canonical closers (</function>, </function_calls>) that some models
	// emit when they confuse Qwen and Anthropic tool-call formats.
	reHybridToolCallBlock = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*</(?:function|function_calls)>`)
	// reNameMarker pulls a tool name out of a JSON-ish prefix even if the
	// surrounding object is malformed (e.g. `{"name": "foo` without a
	// closing quote, or with a stray `>` after the name).
	reNameMarker = regexp.MustCompile(`"name"\s*:\s*"([A-Za-z_][A-Za-z0-9_]*)`)
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

// closeUnclosedToolCallTags auto-closes any <tool_call> tags that weren't closed.
// This handles the common case where Qwen3.7-max opens a tool call but the
// stream cuts off or the model forgets to close it, which would otherwise
// cause the entire tool call to be silently dropped.
//
// Hybrid closers (</function>, </function_calls>) are also counted because
// the model sometimes pairs a <tool_call> opener with a non-canonical closer.
// Without counting those, we'd append a stray </tool_call> at the end of
// text, causing reToolCallBlock to greedily swallow trailing prose.
func closeUnclosedToolCallTags(text string) string {
	opens := strings.Count(text, "<tool_call>")
	closes := strings.Count(text, "</tool_call>")

	// Hybrid closers that pair with <tool_call> in malformed output.
	hybridCloses := strings.Count(text, "</function>") +
		strings.Count(text, "</function_calls>")

	totalCloses := closes + hybridCloses
	if opens <= totalCloses {
		return text
	}

	// Auto-close only the truly unclosed tags.
	unclosed := opens - totalCloses
	return text + strings.Repeat("\n</tool_call>", unclosed)
}

// ParseWithFormats extracts tool calls from text. When multiFormat is true,
// also recognizes Claude-style <function_calls><invoke> blocks and bare JSON.
// Applies hallucination protection to filter invalid/duplicate calls.
func ParseWithFormats(text string, multiFormat bool) ParseResult {
	// Auto-close unclosed  tags before parsing
	text = closeUnclosedToolCallTags(text)

	calls, content := parseToolCallBlocks(text)
	if !multiFormat {
		// Hybrid <tool_call>...</function> blocks are accepted even in
		// single-format mode because they are still anchored on the Qwen
		// <tool_call> opener.
		moreCalls, c := parseHybridToolCallBlocks(content)
		calls = append(calls, moreCalls...)
		content = c
		content = stripOrphanToolCallBlocks(content)
		// Apply hallucination protection
		calls, _ = hallucination.Sanitize(calls)
		return ParseResult{Content: strings.TrimSpace(content), ToolCalls: calls}
	}

	// Claude-style <function_calls> blocks.
	moreCalls, content := parseFunctionCallBlocks(content)
	calls = append(calls, moreCalls...)

	// Hybrid <tool_call>...</function> blocks (model confused two formats).
	moreCalls, content = parseHybridToolCallBlocks(content)
	calls = append(calls, moreCalls...)

	// Bare JSON tool calls (no <tool_call> wrapper).
	moreCalls, content = parseBareJSON(content)
	calls = append(calls, moreCalls...)

	// Safety net: any remaining unparsed <tool_call> blocks shouldn't leak
	// into the displayed text (codex / claude-code render them as garbage).
	content = stripOrphanToolCallBlocks(content)

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
	// Build set of character positions that are inside code fences
	insideFence := buildFenceMask(text)

	var calls []openai.ToolCall
	var content strings.Builder
	lastEnd := 0
	for _, loc := range matches {
		// Skip matches that are inside a code fence
		if loc[0] < len(insideFence) && insideFence[loc[0]] {
			continue
		}
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

// buildFenceMask returns a boolean slice where true means the position
// is inside a code fence (between triple-backtick markers).
func buildFenceMask(text string) []bool {
	mask := make([]bool, len(text))
	inFence := false
	fenceChar := byte('`')
	for i := 0; i < len(text); i++ {
		if i+2 < len(text) && text[i] == fenceChar && text[i+1] == fenceChar && text[i+2] == fenceChar {
			inFence = !inFence
			mask[i] = inFence
			i += 2
			continue
		}
		mask[i] = inFence
	}
	return mask
}

// stripCodeFences removes markdown code fences that wrap the entire block.
// Models sometimes emit ```json ... ``` around tool call JSON, which breaks
// JSON validation. We only strip fences that wrap the entire string to avoid
// touching fences that appear legitimately inside argument values.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if m := reWrappedFence.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return s
}

func parseBlock(inner string) (openai.ToolCall, bool) {
	// Strip code fences first — models sometimes wrap JSON in ```json ```
	inner = stripCodeFences(inner)

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
	if tc, ok := parseHybrid(inner); ok {
		return tc, true
	}
	return openai.ToolCall{}, false
}

// parseHybridToolCallBlocks handles <tool_call> openers paired with
// </function> or </function_calls> closers. The model occasionally
// emits these when it conflates Qwen and Anthropic tool-call syntax.
func parseHybridToolCallBlocks(text string) ([]openai.ToolCall, string) {
	matches := reHybridToolCallBlock.FindAllStringSubmatchIndex(text, -1)
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
			continue
		}
		// Drop the block from output even when parsing fails. Showing the
		// raw malformed tool-call text to the user is strictly worse than
		// dropping it silently — codex / claude-code would render it as
		// noisy prose either way.
	}
	content.WriteString(text[lastEnd:])
	return calls, content.String()
}

// parseHybrid handles the case where the model emitted a hybrid format
// inside a <tool_call> wrapper: a JSON-ish `"name": "foo"` opener
// followed by Anthropic-style <parameter=KEY>VALUE</parameter> blocks.
// e.g. `{"name": "u_shell_command<parameter=command>ls</parameter>...`
func parseHybrid(inner string) (openai.ToolCall, bool) {
	nameMatch := reNameMarker.FindStringSubmatch(inner)
	if nameMatch == nil {
		return openai.ToolCall{}, false
	}
	paramMatches := reParameter.FindAllStringSubmatch(inner, -1)
	if len(paramMatches) == 0 {
		return openai.ToolCall{}, false
	}
	funcName := strings.TrimSpace(nameMatch[1])
	clientName := toolname.FromQwen(funcName)

	params := map[string]string{}
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

// stripOrphanToolCallBlocks removes any leftover <tool_call>...</tool_call>
// blocks from text after all parsers have run. This is a safety net for
// malformed blocks whose inner JSON could not be parsed or repaired —
// rendering the raw block to the client is always worse than dropping it.
func stripOrphanToolCallBlocks(text string) string {
	text = reToolCallBlock.ReplaceAllString(text, "")
	text = reHybridToolCallBlock.ReplaceAllString(text, "")
	return text
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

// HasUnclosedToolCall reports whether text contains an opening <tool_call>
// without a matching closer. Hybrid closers (</function>, </function_calls>)
// are also counted because the model sometimes pairs a <tool_call> opener
// with a non-canonical closer. Without counting those, hybrid-format tool
// calls would falsely trigger continuation.
func HasUnclosedToolCall(s string) bool {
	opens := strings.Count(s, "<tool_call>")
	closes := strings.Count(s, "</tool_call>") +
		strings.Count(s, "</function>") +
		strings.Count(s, "</function_calls>")
	return opens > closes
}

// SawToolMarker reports whether text shows signs the model intended to call
// a tool, even if parsing ultimately failed. Used to distinguish "model
// finished cleanly with no tools" from "model tried to call a tool but the
// output was malformed".
func SawToolMarker(s string) bool {
	return strings.Contains(s, "<tool_call>") ||
		strings.Contains(s, "<function_calls>") ||
		strings.Contains(s, "<invoke")
}
