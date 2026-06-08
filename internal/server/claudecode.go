package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/keaume34/qwen2api/internal/database"
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

// GenerateConversationID generates a deterministic conversation ID.
// For Claude Code: uses IP + User-Agent (session-based)
// For other clients: uses User-Agent + first message
func GenerateConversationID(userAgent, firstUserMessage, clientIP string, isClaudeCode bool) string {
	hash := sha256.New()
	if isClaudeCode {
		// For Claude Code: use IP + User-Agent for session persistence
		hash.Write([]byte(clientIP))
		hash.Write([]byte(userAgent))
	} else {
		// For other clients: use User-Agent + first message
		hash.Write([]byte(userAgent))
		hash.Write([]byte(firstUserMessage))
	}
	return hex.EncodeToString(hash.Sum(nil))[:16] // Use first 16 chars for readability
}

// FindNewMessages finds messages that are not in stored history.
// It compares from the end of stored history to find the delta.
func FindNewMessages(requestMessages []openai.ChatMessage, storedMessages []*database.Message) []openai.ChatMessage {
	if len(storedMessages) == 0 {
		return requestMessages
	}

	// Convert stored messages to comparable format
	storedTexts := make([]string, len(storedMessages))
	for i, m := range storedMessages {
		storedTexts[i] = m.Role + ":" + m.Content
	}

	// Find the last stored message in request messages
	lastStoredIdx := -1
	for i := len(requestMessages) - 1; i >= 0; i-- {
		reqText := requestMessages[i].Role + ":" + requestMessages[i].Text()
		// Check if this message matches the last stored message
		if reqText == storedTexts[len(storedTexts)-1] {
			lastStoredIdx = i
			break
		}
	}

	// If we found the last stored message, return everything after it
	if lastStoredIdx >= 0 && lastStoredIdx < len(requestMessages)-1 {
		return requestMessages[lastStoredIdx+1:]
	}

	// If we couldn't find the match, try matching by content only (more flexible)
	for i := len(requestMessages) - 1; i >= 0; i-- {
		reqContent := requestMessages[i].Text()
		storedContent := storedMessages[len(storedMessages)-1].Content
		if reqContent == storedContent && requestMessages[i].Role == storedMessages[len(storedMessages)-1].Role {
			if i < len(requestMessages)-1 {
				return requestMessages[i+1:]
			}
		}
	}

	// Fallback: return last user message or last message
	if len(requestMessages) > 0 {
		return requestMessages[len(requestMessages)-1:]
	}
	return requestMessages
}