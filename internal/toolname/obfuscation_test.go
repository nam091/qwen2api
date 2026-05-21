package toolname

import "testing"

func TestToQwen_ExplicitAliases(t *testing.T) {
	tests := []struct {
		client string
		want   string
	}{
		{"Read", "fs_open_file"},
		{"Write", "fs_put_file"},
		{"Edit", "fs_patch_file"},
		{"Bash", "shell_run"},
		{"Grep", "text_search"},
		{"Glob", "path_find"},
		{"WebFetch", "http_get_url"},
		{"Agent", "spawn_subagent"},
	}
	for _, tt := range tests {
		got := ToQwen(tt.client)
		if got != tt.want {
			t.Errorf("ToQwen(%q) = %q, want %q", tt.client, got, tt.want)
		}
	}
}

func TestToQwen_AutoPrefix(t *testing.T) {
	tests := []struct {
		client string
		want   string
	}{
		{"CustomTool", "u_CustomTool"},
		{"mcp__playwright__click", "u_mcp__playwright__click"},
		{"SomeRandomTool", "u_SomeRandomTool"},
		{"AskUserQuestion", "u_AskUserQuestion"},
	}
	for _, tt := range tests {
		got := ToQwen(tt.client)
		if got != tt.want {
			t.Errorf("ToQwen(%q) = %q, want %q", tt.client, got, tt.want)
		}
	}
}

func TestToQwen_AlreadySafe(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"fs_open_file", "fs_open_file"},     // Already an alias
		{"u_TaskCreate", "u_TaskCreate"},     // Already prefixed
		{"u_CustomTool", "u_CustomTool"},     // Already prefixed
		{"", ""},                              // Empty
	}
	for _, tt := range tests {
		got := ToQwen(tt.input)
		if got != tt.want {
			t.Errorf("ToQwen(%q) = %q, want %q (should be idempotent)", tt.input, got, tt.want)
		}
	}
}

func TestFromQwen_ReverseAliases(t *testing.T) {
	tests := []struct {
		qwen string
		want string
	}{
		{"fs_open_file", "Read"},
		{"fs_put_file", "Write"},
		{"fs_patch_file", "Edit"},
		{"shell_run", "Bash"},
		{"text_search", "Grep"},
		{"path_find", "Glob"},
		{"http_get_url", "WebFetch"},
		{"spawn_subagent", "Agent"},
	}
	for _, tt := range tests {
		got := FromQwen(tt.qwen)
		if got != tt.want {
			t.Errorf("FromQwen(%q) = %q, want %q", tt.qwen, got, tt.want)
		}
	}
}

func TestFromQwen_StripPrefix(t *testing.T) {
	tests := []struct {
		qwen string
		want string
	}{
		{"u_CustomTool", "CustomTool"},
		{"u_mcp__playwright__click", "mcp__playwright__click"},
		{"u_SomeRandomTool", "SomeRandomTool"},
		{"u_AskUserQuestion", "AskUserQuestion"},
	}
	for _, tt := range tests {
		got := FromQwen(tt.qwen)
		if got != tt.want {
			t.Errorf("FromQwen(%q) = %q, want %q", tt.qwen, got, tt.want)
		}
	}
}

func TestFromQwen_Passthrough(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"UnknownTool", "UnknownTool"}, // No mapping, return as-is
		{"", ""},                        // Empty
	}
	for _, tt := range tests {
		got := FromQwen(tt.input)
		if got != tt.want {
			t.Errorf("FromQwen(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	tests := []string{
		"Read",
		"Write",
		"TaskCreate",
		"CustomTool",
		"mcp__playwright__click",
		"AskUserQuestion",
	}
	for _, original := range tests {
		qwen := ToQwen(original)
		back := FromQwen(qwen)
		if back != original {
			t.Errorf("Round-trip failed: %q → %q → %q", original, qwen, back)
		}
	}
}
