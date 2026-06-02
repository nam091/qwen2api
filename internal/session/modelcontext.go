package session

import "strings"

// modelContextWindows maps model name patterns to their context window sizes.
// Patterns are matched with strings.Contains (case-insensitive).
// Order matters: more specific patterns should come first.
var modelContextWindows = []struct {
	pattern string
	tokens  int
}{
	// Qwen-Long: 10M context
	{"qwen-long", 10_000_000},
	// Qwen 3.7 series: 1M context
	{"qwen3.7-max", 1_000_000},
	{"qwen3.7-plus", 1_000_000},
	// Qwen 3.6 series: 1M context
	{"qwen3.6-plus", 1_000_000},
	{"qwen3.6-max", 1_000_000},
	{"qwen3.6-flash", 1_000_000},
	{"qwen3.6-27b", 131_072},
	{"qwen3.6-35b", 131_072},
	// Qwen 3.5 series: 1M context
	{"qwen3.5-plus", 1_000_000},
	{"qwen3.5-max", 1_000_000},
	{"qwen3.5-flash", 1_000_000},
	{"qwen3.5-omni-plus", 1_000_000},
	{"qwen3.5-omni-flash", 1_000_000},
	{"qwen3.5-397b", 131_072},
	{"qwen3.5-122b", 131_072},
	{"qwen3.5-35b", 131_072},
	{"qwen3.5-27b", 131_072},
	// Qwen 3 series
	{"qwen3-max", 131_072},
	{"qwen3-plus", 131_072},
	{"qwen3-coder", 131_072},
	{"qwen3-vl", 131_072},
	{"qwen3-omni", 131_072},
	// Qwen legacy / turbo / flash
	{"qwen-plus", 131_072},
	{"qwen-turbo", 131_072},
	{"qwen-flash", 131_072},
	{"qwen-max", 32_768},
	// Preview/beta models inherit from parent series
	{"qwen-latest-series", 1_000_000},
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
