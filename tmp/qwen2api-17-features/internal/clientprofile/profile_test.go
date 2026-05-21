package clientprofile

import (
	"net/http"
	"testing"
)

func TestDetectCursor(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "Cursor/1.0")
	p := Detect(h, nil)
	if p != Cursor {
		t.Errorf("expected Cursor, got %s", p)
	}
}

func TestDetectClaudeCode(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "claude-code/1.0")
	p := Detect(h, nil)
	if p != ClaudeCode {
		t.Errorf("expected ClaudeCode, got %s", p)
	}
}

func TestDetectClaudeCodeByTools(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "python-requests/2.31")
	tools := []string{"Read", "Write", "Bash", "Edit", "Glob"}
	p := Detect(h, tools)
	if p != ClaudeCode {
		t.Errorf("expected ClaudeCode by tools, got %s", p)
	}
}

func TestDetectQwenCode(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "something")
	tools := []string{"read_file", "list_directory", "write_file", "run_shell_command"}
	p := Detect(h, tools)
	if p != QwenCode {
		t.Errorf("expected QwenCode, got %s", p)
	}
}

func TestDetectGeneric(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "my-app/1.0")
	p := Detect(h, nil)
	if p != Generic {
		t.Errorf("expected Generic, got %s", p)
	}
}

func TestIsHeavyToolProfile(t *testing.T) {
	if !IsHeavyToolProfile(ClaudeCode) {
		t.Error("ClaudeCode should be heavy tool profile")
	}
	if IsHeavyToolProfile(Generic) {
		t.Error("Generic should not be heavy tool profile")
	}
}
