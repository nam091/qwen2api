package fewshot

import "testing"

func TestExtractNamespace(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"mcp__playwright__click", "mcp__playwright"},
		{"mcp__github__pr", "mcp__github"},
		{"read", ""},
		{"some_tool", ""},
	}
	for _, tt := range tests {
		got := ExtractNamespace(tt.name)
		if got != tt.want {
			t.Errorf("ExtractNamespace(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestIsCoreTool(t *testing.T) {
	if !IsCoreTool("Read") {
		t.Error("Read should be core")
	}
	if !IsCoreTool("bash") {
		t.Error("bash should be core")
	}
	if IsCoreTool("mcp__foo__bar") {
		t.Error("mcp tool should not be core")
	}
}

func TestPickRepresentatives(t *testing.T) {
	tools := []Tool{
		{Name: "read", Description: "read file"},
		{Name: "mcp__playwright__click", Description: "click element"},
		{Name: "mcp__playwright__type", Description: "type text"},
		{Name: "mcp__github__pr", Description: "create PR"},
		{Name: "mcp__slack__send", Description: "send message"},
		{Name: "mcp__jira__issue", Description: "create issue"},
	}
	reps := PickRepresentatives(tools, 3)
	if len(reps) == 0 {
		t.Fatal("expected at least 1 representative")
	}
	if len(reps) > 4 { // 1 core + 3 third-party
		t.Errorf("expected ≤4, got %d", len(reps))
	}
}

func TestRenderFewShot(t *testing.T) {
	tools := []Tool{
		{Name: "mcp__github__pr", Description: "create PR", Parameters: map[string]any{
			"properties": map[string]any{
				"title": map[string]any{"type": "string"},
				"body":  map[string]any{"type": "string"},
			},
		}},
	}
	turns := RenderFewShot(tools)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Role != "user" || turns[1].Role != "assistant" {
		t.Error("expected user/assistant pair")
	}
}
