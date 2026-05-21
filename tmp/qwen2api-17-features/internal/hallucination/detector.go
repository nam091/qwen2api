// Package hallucination provides detection and prevention of invalid/duplicate
// tool calls that LLMs sometimes generate.
package hallucination

import (
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
)

// RefusalPatterns are phrases that indicate the model is refusing to call tools.
var RefusalPatterns = []string{
	"I cannot",
	"I'm unable to",
	"I can't",
	"I am not able to",
	"I don't have access",
	"I cannot access",
	"I'm not allowed",
	"I apologize, but I cannot",
	"Sorry, I cannot",
	"Unfortunately, I cannot",
	"auto_agent_blocked",
	"Tool does not exist",
	"Tool X does not exist",
}

// BlockedResponse indicates Qwen blocked the tool call.
type BlockedResponse struct {
	Blocked bool
	Reason  string
}

// DetectRefusal checks if the response text contains refusal patterns.
func DetectRefusal(text string) bool {
	lower := strings.ToLower(text)
	for _, pattern := range RefusalPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// DetectBlocked checks if Qwen explicitly blocked the tool call.
func DetectBlocked(text string) BlockedResponse {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "auto_agent_blocked") {
		return BlockedResponse{Blocked: true, Reason: "auto_agent_blocked"}
	}
	if strings.Contains(lower, "tool does not exist") || strings.Contains(lower, "tool x does not exist") {
		return BlockedResponse{Blocked: true, Reason: "tool_not_found"}
	}
	return BlockedResponse{Blocked: false}
}

// DetectDuplicates finds duplicate tool calls in the same response.
// Returns indices of duplicate calls (keep first occurrence).
func DetectDuplicates(calls []openai.ToolCall) []int {
	seen := make(map[string]int) // signature -> first index
	var duplicates []int

	for i, call := range calls {
		// Signature: name + arguments (normalized)
		sig := call.Function.Name + ":" + call.Function.Arguments
		if firstIdx, exists := seen[sig]; exists {
			// Duplicate found
			duplicates = append(duplicates, i)
			_ = firstIdx // Keep first occurrence
		} else {
			seen[sig] = i
		}
	}

	return duplicates
}

// FilterDuplicates removes duplicate tool calls, keeping first occurrence.
func FilterDuplicates(calls []openai.ToolCall) []openai.ToolCall {
	if len(calls) <= 1 {
		return calls
	}

	duplicates := DetectDuplicates(calls)
	if len(duplicates) == 0 {
		return calls
	}

	// Build set of indices to remove
	removeSet := make(map[int]bool)
	for _, idx := range duplicates {
		removeSet[idx] = true
	}

	// Filter out duplicates
	filtered := make([]openai.ToolCall, 0, len(calls)-len(duplicates))
	for i, call := range calls {
		if !removeSet[i] {
			filtered = append(filtered, call)
		}
	}

	return filtered
}

// ValidateToolCall checks if a tool call is valid (non-empty name and arguments).
func ValidateToolCall(call openai.ToolCall) bool {
	if call.Function.Name == "" {
		return false
	}
	if call.Function.Arguments == "" || call.Function.Arguments == "null" {
		return false
	}
	return true
}

// FilterInvalid removes invalid tool calls (empty name/arguments).
func FilterInvalid(calls []openai.ToolCall) []openai.ToolCall {
	if len(calls) == 0 {
		return calls
	}

	filtered := make([]openai.ToolCall, 0, len(calls))
	for _, call := range calls {
		if ValidateToolCall(call) {
			filtered = append(filtered, call)
		}
	}

	return filtered
}

// Sanitize applies all hallucination protections:
// 1. Remove invalid calls (empty name/args)
// 2. Remove duplicate calls
// Returns sanitized calls and whether any were filtered.
func Sanitize(calls []openai.ToolCall) ([]openai.ToolCall, bool) {
	original := len(calls)
	calls = FilterInvalid(calls)
	calls = FilterDuplicates(calls)
	filtered := original - len(calls)
	return calls, filtered > 0
}
