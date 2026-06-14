// Package hallucination provides detection and prevention of invalid/duplicate
// tool calls that LLMs sometimes generate.
package hallucination

import (
	"encoding/json"
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
	"does not exists",
	"does not exist",
	"not available",
	"not found",
	"unknown tool",
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

// ValidateToolCall checks if a tool call is valid (non-empty name and valid JSON arguments).
func ValidateToolCall(call openai.ToolCall) bool {
	if call.Function.Name == "" {
		return false
	}
	if call.Function.Arguments == "" || call.Function.Arguments == "null" {
		return false
	}
	// Arguments must be valid JSON
	if !json.Valid([]byte(call.Function.Arguments)) {
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

// StripToolErrorMessages removes tool-related error messages from response text.
// When the model generates both tool calls and error messages about non-existent
// tools, this function strips the error messages while keeping the useful content.
func StripToolErrorMessages(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			result = append(result, line)
			continue
		}
		// Skip lines that are tool error messages
		isError := false
		for _, pattern := range RefusalPatterns {
			if strings.Contains(strings.ToLower(trimmed), strings.ToLower(pattern)) {
				isError = true
				break
			}
		}
		// Also skip lines that look like "Tool X does not exists"
		if strings.Contains(strings.ToLower(trimmed), "tool") && 
		   (strings.Contains(strings.ToLower(trimmed), "does not") || 
		    strings.Contains(strings.ToLower(trimmed), "not available") ||
		    strings.Contains(strings.ToLower(trimmed), "not found")) {
			isError = true
		}
		if !isError {
			result = append(result, line)
		}
	}
	cleaned := strings.Join(result, "\n")
	// Handle concatenated error messages without newlines (e.g. "Tool X does not exists.Tool Y does not exists.")
	// Split by "Tool" prefix and filter out error fragments
	cleaned = stripConcatenatedToolErrors(cleaned)
	return cleaned
}

// stripConcatenatedToolErrors handles cases where Qwen outputs multiple
// "Tool X does not exists" messages concatenated without newlines.
func stripConcatenatedToolErrors(text string) string {
	// Pattern: "Tool <name> does not exists." repeated without spaces/newlines
	// Split by sentence boundaries and filter
	lower := strings.ToLower(text)
	
	// Find all "Tool ... does not" segments
	segments := splitToolErrors(text, lower)
	if len(segments) == 0 {
		return text
	}
	
	// Reassemble non-error segments
	var clean []string
	for _, seg := range segments {
		if !seg.isError {
			clean = append(clean, seg.text)
		}
	}
	result := strings.TrimSpace(strings.Join(clean, " "))
	return result
}

type textSegment struct {
	text    string
	isError bool
}

func splitToolErrors(text, lower string) []textSegment {
	var segments []textSegment
	i := 0
	for i < len(text) {
		// Look for "Tool" followed by error indicators
		if i+4 <= len(text) && lower[i:i+4] == "tool" {
			// Find the end of this error message (next "Tool" or end of string)
			end := i + 4
			for end < len(text) {
				if end+4 <= len(text) && lower[end:end+4] == "tool" {
					break
				}
				end++
			}
			fragment := text[i:end]
			fragLower := lower[i:end]
			if strings.Contains(fragLower, "does not") || 
			   strings.Contains(fragLower, "not available") || 
			   strings.Contains(fragLower, "not found") {
				segments = append(segments, textSegment{text: fragment, isError: true})
			} else {
				segments = append(segments, textSegment{text: fragment, isError: false})
			}
			i = end
			continue
		}
		// Non-"Tool" text
		end := i + 1
		for end < len(text) {
			if end+4 <= len(text) && lower[end:end+4] == "tool" {
				break
			}
			end++
		}
		segments = append(segments, textSegment{text: text[i:end], isError: false})
		i = end
	}
	return segments
}
