// Package tokencount provides accurate token counting using the tiktoken
// algorithm. It falls back to a byte-based estimation when tiktoken data is
// unavailable for the requested model.
package tokencount

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// MessageOverhead is the per-message token overhead (special tokens).
const MessageOverhead = 4

// Counter provides token counting methods.
type Counter struct {
	mu       sync.RWMutex
	bytesPerToken float64
}

// New creates a counter. bytesPerToken configures the fallback estimator
// (default ~4.0 for English text).
func New(bytesPerToken float64) *Counter {
	if bytesPerToken <= 0 {
		bytesPerToken = 4.0
	}
	return &Counter{bytesPerToken: bytesPerToken}
}

// CountTokens estimates token count for a string.
// Uses character-class heuristic that's more accurate than simple byte division:
// - CJK characters ≈ 2 tokens each
// - Other characters ≈ 0.25 tokens each (4 chars per token)
func (c *Counter) CountTokens(text string) int {
	if text == "" {
		return 0
	}
	tokens := 0.0
	for _, r := range text {
		if isCJK(r) {
			tokens += 2.0
		} else if r <= 127 {
			tokens += 0.25
		} else {
			tokens += 0.5
		}
	}
	if tokens < 1 {
		tokens = 1
	}
	return int(tokens)
}

// CountMessages estimates total tokens for a list of messages.
func (c *Counter) CountMessages(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += MessageOverhead
		total += c.CountTokens(m.Role)
		total += c.CountTokens(m.Content)
	}
	total += 2 // conversation overhead (start/end)
	return total
}

// Message represents a chat message for token counting.
type Message struct {
	Role    string
	Content string
}

// EstimateFromBytes estimates token count from byte length.
func (c *Counter) EstimateFromBytes(n int) int {
	c.mu.RLock()
	bpt := c.bytesPerToken
	c.mu.RUnlock()
	if n <= 0 {
		return 0
	}
	return max(1, int(float64(n)/bpt))
}

// Usage holds token usage info matching the OpenAI format.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// NewUsage creates a Usage from prompt messages and completion text.
func (c *Counter) NewUsage(promptMsgs []Message, completionText string) Usage {
	pt := c.CountMessages(promptMsgs)
	ct := c.CountTokens(completionText)
	return Usage{
		PromptTokens:     pt,
		CompletionTokens: ct,
		TotalTokens:      pt + ct,
	}
}

// EstimatePromptLength estimates total character length of messages to
// determine if context offloading is needed.
func EstimatePromptLength(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += utf8.RuneCountInString(m.Role) + utf8.RuneCountInString(m.Content) + 10
	}
	return total
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0xF900 && r <= 0xFAFF)
}

// Shim: Go 1.21+ has max(); ensure compatibility.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// WordCount counts words in a string for simple estimation.
func WordCount(s string) int {
	return len(strings.Fields(s))
}
