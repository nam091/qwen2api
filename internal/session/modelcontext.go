package session

import "strings"

// modelContextWindows maps model name patterns to their context window sizes.
// Patterns are matched with strings.Contains (case-insensitive).
// Order matters: more specific patterns should come first.
var modelContextWindows = []struct {
	pattern string
	tokens  int
}{
	{"qwen-long", 10_000_000},
	{"qwen3.7-max", 1_000_000},
	{"qwen3.6-plus", 1_000_000},
	{"qwen3.6-flash", 1_000_000},
	{"qwen3.5-plus", 1_000_000},
	{"qwen3.5-flash", 1_000_000},
	{"qwen3-max", 256_000},
	{"qwen-plus", 1_000_000},
	{"qwen-turbo", 1_000_000},
	{"qwen-flash", 1_000_000},
	{"qwen-max", 128_000},
}

// GetContextWindow returns the context window size for a given model name.
// Falls back to defaultFallback if no pattern matches.
func GetContextWindow(model string, defaultFallback int) int {
	if defaultFallback <= 0 {
		defaultFallback = 32768
	}
	lower := strings.ToLower(model)
	for _, entry := range modelContextWindows {
		if strings.Contains(lower, entry.pattern) {
			return entry.tokens
		}
	}
	return defaultFallback
}
