package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/tokencount"
)

// CompactResult holds the result of a compaction operation.
type CompactResult struct {
	Compacted    []openai.ChatMessage
	WasCompacted bool
	OriginalLen  int
	CompactLen   int
}

// AutoCompact checks if messages exceed the context window threshold.
// If so, keeps system messages + recent messages and summarizes the middle portion.
// maxTokens is the context window size, threshold is the fraction (0-1) that triggers compaction.
func AutoCompact(msgs []openai.ChatMessage, maxTokens int, threshold float64, counter *tokencount.Counter) CompactResult {
	if maxTokens <= 0 {
		maxTokens = 32768
	}
	if threshold <= 0 || threshold > 1 {
		threshold = 0.8
	}
	if counter == nil {
		counter = tokencount.New(4.0)
	}

	totalTokens := 0
	for _, m := range msgs {
		totalTokens += counter.CountTokens(m.Role) + counter.CountTokens(m.Text()) + tokencount.MessageOverhead
	}

	limit := int(float64(maxTokens) * threshold)

	result := CompactResult{
		Compacted:   msgs,
		OriginalLen: totalTokens,
		CompactLen:  totalTokens,
	}

	if totalTokens <= limit {
		return result
	}

	// Need compaction. Strategy:
	// 1. Keep all system messages
	// 2. Keep the last N messages (recent context)
	// 3. Summarize middle messages into a single "Context" message

	var systemMsgs []openai.ChatMessage
	var nonSystemMsgs []openai.ChatMessage
	for _, m := range msgs {
		if strings.EqualFold(m.Role, "system") {
			systemMsgs = append(systemMsgs, m)
		} else {
			nonSystemMsgs = append(nonSystemMsgs, m)
		}
	}

	// Calculate how many recent messages we can keep
	recentBudget := int(float64(limit) * 0.6)
	systemTokens := 0
	for _, m := range systemMsgs {
		systemTokens += counter.CountTokens(m.Role) + counter.CountTokens(m.Text()) + tokencount.MessageOverhead
	}

	keepCount := 0
	recentTokens := 0
	for i := len(nonSystemMsgs) - 1; i >= 0; i-- {
		msgTokens := counter.CountTokens(nonSystemMsgs[i].Role) + counter.CountTokens(nonSystemMsgs[i].Text()) + tokencount.MessageOverhead
		if recentTokens+msgTokens > recentBudget && keepCount > 0 {
			break
		}
		recentTokens += msgTokens
		keepCount++
	}
	if keepCount == 0 {
		keepCount = 1
	}

	middleMsgs := nonSystemMsgs[:len(nonSystemMsgs)-keepCount]
	recentMsgs := nonSystemMsgs[len(nonSystemMsgs)-keepCount:]

	// Build context summary from middle messages
	var contextParts []string
	for _, m := range middleMsgs {
		text := m.Text()
		if len(text) > 500 {
			text = text[:500] + "..."
		}
		contextParts = append(contextParts, fmt.Sprintf("[%s]: %s", m.Role, text))
	}
	contextSummary := fmt.Sprintf("[Previous conversation context - %d messages compacted]\n%s", len(middleMsgs), strings.Join(contextParts, "\n"))

	compacted := make([]openai.ChatMessage, 0, len(systemMsgs)+1+len(recentMsgs))
	compacted = append(compacted, systemMsgs...)
	summaryBytes, _ := json.Marshal(contextSummary)
	compacted = append(compacted, openai.ChatMessage{
		Role:    "system",
		Content: summaryBytes,
	})
	compacted = append(compacted, recentMsgs...)

	finalTokens := 0
	for _, m := range compacted {
		finalTokens += counter.CountTokens(m.Role) + counter.CountTokens(m.Text()) + tokencount.MessageOverhead
	}

	result.Compacted = compacted
	result.WasCompacted = true
	result.CompactLen = finalTokens
	return result
}
