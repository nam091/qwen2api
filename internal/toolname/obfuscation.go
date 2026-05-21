// Package toolname provides tool name obfuscation to avoid Qwen internal
// validation rejection. Qwen rejects tool calls when client tool names collide
// with its internal function names.
//
// Strategy:
// 1. Explicit aliases for high-value short names (Read→fs_open_file, etc.)
// 2. Auto-prefix fallback: add "u_" prefix to all other tools (TaskCreate→u_TaskCreate)
//
// Outbound (to Qwen): client name → alias/prefixed name
// Inbound (from Qwen): alias/prefixed name → client name
package toolname

// Aliases maps common tool names to Qwen-safe alternatives.
var Aliases = map[string]string{
	"Read":         "fs_open_file",
	"Write":        "fs_put_file",
	"Edit":         "fs_patch_file",
	"Bash":         "shell_run",
	"PowerShell":   "shell_run_ps",
	"Grep":         "text_search",
	"Glob":         "path_find",
	"NotebookEdit": "notebook_patch",
	"WebFetch":     "http_get_url",
	"WebSearch":    "web_query",
	"Agent":        "spawn_subagent",
	"TaskCreate":   "task_new",
	"TaskUpdate":   "task_modify",
	"TaskGet":      "task_read",
	"TaskList":     "task_list_all",
}

// reverseAliases is built at init time for fast reverse lookup.
var reverseAliases map[string]string

const autoPrefix = "u_"

func init() {
	reverseAliases = make(map[string]string, len(Aliases))
	for client, qwen := range Aliases {
		reverseAliases[qwen] = client
	}
}

// ToQwen converts a client tool name to a Qwen-safe name.
// - If explicit alias exists: use alias (Read → fs_open_file)
// - Otherwise: add u_ prefix (TaskCreate → u_TaskCreate)
// - Empty/already-safe: return as-is
func ToQwen(name string) string {
	if name == "" {
		return name
	}
	if alias, ok := Aliases[name]; ok {
		return alias
	}
	// Already a Qwen-safe name (avoid double-prefix)
	if _, ok := reverseAliases[name]; ok {
		return name
	}
	if len(name) >= len(autoPrefix) && name[:len(autoPrefix)] == autoPrefix {
		return name
	}
	return autoPrefix + name
}

// FromQwen converts a Qwen-returned name back to the client's original name.
// - If reverse alias exists: map back (fs_open_file → Read)
// - If has u_ prefix: strip it (u_TaskCreate → TaskCreate)
// - Otherwise: return as-is (compatibility with Qwen returning original names)
func FromQwen(name string) string {
	if name == "" {
		return name
	}
	if client, ok := reverseAliases[name]; ok {
		return client
	}
	if len(name) >= len(autoPrefix) && name[:len(autoPrefix)] == autoPrefix {
		return name[len(autoPrefix):]
	}
	return name
}
