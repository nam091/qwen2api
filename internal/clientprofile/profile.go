// Package clientprofile detects the IDE/client type from HTTP headers and tool
// definitions. It allows the proxy to customize its behaviour (prompt format,
// tool call encoding) per client.
package clientprofile

import (
	"net/http"
	"strings"
)

// Profile identifies the calling client.
type Profile string

const (
	ClaudeCode Profile = "claude_code"
	OpenClaw   Profile = "openclaw"
	QwenCode   Profile = "qwen_code"
	Cursor     Profile = "cursor"
	Generic    Profile = "generic"
)

var qwenCodeToolNames = map[string]struct{}{
	"read_file":         {},
	"list_directory":    {},
	"write_file":        {},
	"run_shell_command": {},
}

var codeToolHints = map[string]struct{}{
	"read": {}, "write": {}, "edit": {}, "multiedit": {},
	"bash": {}, "grep": {}, "glob": {}, "listdir": {},
	"notebookedit": {}, "searchfiles": {},
}

// Detect determines the client profile from request headers and tool names.
func Detect(headers http.Header, toolNames []string) Profile {
	ua := strings.ToLower(headers.Get("User-Agent"))
	clientUA := strings.ToLower(headers.Get("X-Openai-Client-User-Agent"))

	// Check for Cursor
	if strings.Contains(ua, "cursor") || strings.Contains(clientUA, "cursor") {
		return Cursor
	}

	// Check for Qwen Code
	if hasQwenCodeHint(ua, clientUA) || matchesQwenCodeTools(toolNames) {
		return QwenCode
	}

	// Check for Claude Code
	if isClaudeCodeRequest(ua, clientUA, toolNames) {
		return ClaudeCode
	}

	// Check for OpenClaw
	if strings.Contains(ua, "openclaw") || strings.Contains(clientUA, "openclaw") {
		return OpenClaw
	}

	// Check for any OpenAI SDK fingerprint
	if headers.Get("X-Stainless-Lang") != "" || headers.Get("X-Stainless-Package-Version") != "" {
		return ClaudeCode // OpenAI SDK clients default to Claude Code profile
	}

	return Generic
}

func hasQwenCodeHint(ua, clientUA string) bool {
	for _, v := range []string{ua, clientUA} {
		if strings.Contains(v, "qwen") && strings.Contains(v, "code") {
			return true
		}
	}
	return false
}

func matchesQwenCodeTools(toolNames []string) bool {
	matches := 0
	for _, name := range toolNames {
		if _, ok := qwenCodeToolNames[strings.ToLower(name)]; ok {
			matches++
		}
	}
	return matches >= len(qwenCodeToolNames)
}

func isClaudeCodeRequest(ua, clientUA string, toolNames []string) bool {
	if strings.Contains(ua, "claude") || strings.Contains(clientUA, "claude") {
		return true
	}
	// Heuristic: if tools include Read, Write, Bash, Edit -> likely Claude Code
	codeHits := 0
	for _, name := range toolNames {
		lower := strings.ToLower(name)
		if _, ok := codeToolHints[lower]; ok {
			codeHits++
		}
	}
	return codeHits >= 3
}

// IsHeavyToolProfile returns true for profiles that use many tools and benefit
// from aggressive compression.
func IsHeavyToolProfile(p Profile) bool {
	return p == ClaudeCode || p == QwenCode
}

// ToolCallFormat returns the appropriate tool call delimiter for the profile.
func ToolCallFormat(p Profile) (open, close string) {
	switch p {
	case ClaudeCode:
		return "##TOOL_CALL##\n", "\n##END_CALL##"
	default:
		return "<tool_call>\n", "\n</tool_call>"
	}
}
