package server

import (
	"fmt"
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
)

// ClaudeCodeOptimizer handles smart message extraction for Claude Code requests.
type ClaudeCodeOptimizer struct {
	enabled bool
}

// NewClaudeCodeOptimizer creates a new optimizer instance.
func NewClaudeCodeOptimizer(enabled bool) *ClaudeCodeOptimizer {
	return &ClaudeCodeOptimizer{enabled: enabled}
}

// ExtractNewContent extracts only the new content from Claude Code messages.
// This reduces token usage by sending only necessary content to upstream.
func (o *ClaudeCodeOptimizer) ExtractNewContent(msgs []openai.ChatMessage) string {
	if !o.enabled || len(msgs) == 0 {
		return collapseMessages(msgs)
	}

	// If only 1 message, return as-is
	if len(msgs) == 1 {
		return msgs[0].Text()
	}

	// Get the last message (regardless of role)
	lastMsg := msgs[len(msgs)-1]
	lastRole := strings.ToLower(lastMsg.Role)

	// If last message is tool output, return just that
	if lastRole == "tool" {
		return lastMsg.Text()
	}

	// If last message is assistant with tool calls, return formatted tool calls
	if lastRole == "assistant" && len(lastMsg.ToolCalls) > 0 {
		return o.formatToolCalls(lastMsg.ToolCalls)
	}

	// If last message is assistant with content, return just that
	if lastRole == "assistant" {
		if content := lastMsg.Text(); content != "" {
			return content
		}
	}

	// If last message is user, return just that
	if lastRole == "user" {
		return lastMsg.Text()
	}

	// Fallback: collapse all messages
	return collapseMessages(msgs)
}

// formatToolCalls formats tool calls into a readable string.
func (o *ClaudeCodeOptimizer) formatToolCalls(toolCalls []openai.ToolCall) string {
	var b strings.Builder
	for i, tc := range toolCalls {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("<tool_call>\n")
		b.WriteString(fmt.Sprintf(`{"name": "%s", "arguments": %s}`, tc.Function.Name, tc.Function.Arguments))
		b.WriteString("\n</tool_call>")
	}
	return b.String()
}

// ShouldOptimize returns true if the request should be optimized.
func (o *ClaudeCodeOptimizer) ShouldOptimize(clientType ClientType, conversationID string) bool {
	return o.enabled && clientType == ClientClaudeCode && conversationID != ""
}

// GetOptimizationStats returns statistics about the optimization.
func (o *ClaudeCodeOptimizer) GetOptimizationStats(original, optimized string) map[string]interface{} {
	originalTokens := len(original) / 4  // Rough estimate
	optimizedTokens := len(optimized) / 4
	savedTokens := originalTokens - optimizedTokens
	savedPercent := 0.0
	if originalTokens > 0 {
		savedPercent = float64(savedTokens) / float64(originalTokens) * 100
	}

	return map[string]interface{}{
		"original_tokens":  originalTokens,
		"optimized_tokens": optimizedTokens,
		"saved_tokens":     savedTokens,
		"saved_percent":    savedPercent,
	}
}