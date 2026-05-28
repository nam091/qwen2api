package server

import (
	"net/http/httptest"
	"testing"
)

func TestDetectClient(t *testing.T) {
	tests := []struct {
		name     string
		ua       string
		xApp     string
		xTitle   string
		expected ClientType
	}{
		{
			name:     "Codex via User-Agent",
			ua:       "codex-cli/1.0",
			expected: ClientCodex,
		},
		{
			name:     "Codex via x-app header",
			ua:       "",
			xApp:     "codex",
			expected: ClientCodex,
		},
		{
			name:     "Claude Code via User-Agent",
			ua:       "claude-code/1.0",
			expected: ClientClaudeCode,
		},
		{
			name:     "Claude via Anthropic UA",
			ua:       "anthropic-claude/2.0",
			expected: ClientClaudeCode,
		},
		{
			name:     "OpenCode via User-Agent",
			ua:       "opencode/0.1",
			expected: ClientOpenCode,
		},
		{
			name:     "Cursor via User-Agent",
			ua:       "cursor/1.0",
			expected: ClientCursor,
		},
		{
			name:     "Aider via User-Agent",
			ua:       "aider/0.1",
			expected: ClientAider,
		},
		{
			name:     "Cline via User-Agent",
			ua:       "cline/1.0",
			expected: ClientCline,
		},
		{
			name:     "Unknown client",
			ua:       "curl/7.0",
			expected: ClientUnknown,
		},
		{
			name:     "Codex via x-title",
			xTitle:   "Codex CLI",
			expected: ClientCodex,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tt.ua != "" {
				r.Header.Set("User-Agent", tt.ua)
			}
			if tt.xApp != "" {
				r.Header.Set("x-app", tt.xApp)
			}
			if tt.xTitle != "" {
				r.Header.Set("x-title", tt.xTitle)
			}

			result := DetectClient(r)
			if result != tt.expected {
				t.Errorf("DetectClient() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsCLITool(t *testing.T) {
	tests := []struct {
		client   ClientType
		expected bool
	}{
		{ClientCodex, true},
		{ClientClaudeCode, true},
		{ClientOpenCode, true},
		{ClientAider, true},
		{ClientCline, true},
		{ClientCursor, false},
		{ClientUnknown, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.client), func(t *testing.T) {
			result := IsCLITool(tt.client)
			if result != tt.expected {
				t.Errorf("IsCLITool(%v) = %v, want %v", tt.client, result, tt.expected)
			}
		})
	}
}

func TestDetectClientCaseInsensitive(t *testing.T) {
	tests := []struct {
		name     string
		ua       string
		expected ClientType
	}{
		{"uppercase CODEX", "CODEX/1.0", ClientCodex},
		{"mixed case Claude-Code", "Claude-Code/1.0", ClientClaudeCode},
		{"lowercase opencode", "opencode/0.1", ClientOpenCode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("User-Agent", tt.ua)

			result := DetectClient(r)
			if result != tt.expected {
				t.Errorf("DetectClient() = %v, want %v", result, tt.expected)
			}
		})
	}
}
