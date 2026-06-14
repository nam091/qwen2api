package server

import (
	"net/http"
	"strings"
)

// ClientType identifies the CLI tool or client making the request.
type ClientType string

const (
	ClientUnknown    ClientType = "unknown"
	ClientCodex      ClientType = "codex"
	ClientClaudeCode ClientType = "claude-code"
	ClientOpenCode   ClientType = "opencode"
	ClientCursor     ClientType = "cursor"
	ClientAider      ClientType = "aider"
	ClientCline      ClientType = "cline"
)

// DetectClient identifies the CLI tool from request headers.
// Inspired by 9router's clientDetector.js pattern.
func DetectClient(r *http.Request) ClientType {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	xApp := strings.ToLower(r.Header.Get("x-app"))
	xTitle := strings.ToLower(r.Header.Get("x-title"))

	// Codex CLI
	if strings.Contains(ua, "codex") || strings.Contains(xApp, "codex") {
		return ClientCodex
	}
	if strings.Contains(ua, "openai-codex") {
		return ClientCodex
	}

	// Claude Code — check multiple patterns
	if strings.Contains(ua, "claude-code") || strings.Contains(xApp, "claude-code") {
		return ClientClaudeCode
	}
	if strings.Contains(ua, "anthropic") && strings.Contains(ua, "claude") {
		return ClientClaudeCode
	}
	// Claude Code may send just "claude" in x-app
	if strings.Contains(xApp, "claude") {
		return ClientClaudeCode
	}
	// Check for anthropic-specific headers (Claude Code sends these)
	if r.Header.Get("anthropic-version") != "" {
		return ClientClaudeCode
	}

	// OpenCode
	if strings.Contains(ua, "opencode") || strings.Contains(xApp, "opencode") {
		return ClientOpenCode
	}

	// Cursor
	if strings.Contains(ua, "cursor") || strings.Contains(xApp, "cursor") {
		return ClientCursor
	}

	// Aider
	if strings.Contains(ua, "aider") {
		return ClientAider
	}

	// Cline
	if strings.Contains(ua, "cline") || strings.Contains(xApp, "cline") {
		return ClientCline
	}

	// Check x-title as fallback
	if strings.Contains(xTitle, "codex") {
		return ClientCodex
	}
	if strings.Contains(xTitle, "claude") {
		return ClientClaudeCode
	}
	if strings.Contains(xTitle, "opencode") {
		return ClientOpenCode
	}

	// Check path-based detection: Claude Code always hits /v1/messages
	if strings.HasPrefix(r.URL.Path, "/v1/messages") {
		return ClientClaudeCode
	}

	return ClientUnknown
}

// IsCLITool returns true if the client is a known CLI tool.
func IsCLITool(c ClientType) bool {
	switch c {
	case ClientCodex, ClientClaudeCode, ClientOpenCode, ClientAider, ClientCline:
		return true
	}
	return false
}
