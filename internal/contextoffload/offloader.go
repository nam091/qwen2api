// Package contextoffload handles dynamic context management for long
// conversations. When the estimated prompt exceeds configurable thresholds,
// it offloads older messages to file attachments (via OSS upload) to stay
// within upstream token limits while preserving context.
package contextoffload

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Mode describes how context is handled.
type Mode string

const (
	Inline Mode = "inline" // all messages sent as-is
	Hybrid Mode = "hybrid" // latest user + recent history inline, old history as file
	File   Mode = "file"   // entire history offloaded to file, only latest user inline
)

// Thresholds for mode selection (in estimated characters).
const (
	DefaultHybridThreshold = 30000  // ~7.5k tokens
	DefaultFileThreshold   = 80000  // ~20k tokens
)

// Config configures the offloader.
type Config struct {
	HybridThreshold int // chars above which hybrid mode kicks in
	FileThreshold   int // chars above which full file mode kicks in
	MaxRecentMessages int // messages to keep inline in hybrid mode
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		HybridThreshold:   DefaultHybridThreshold,
		FileThreshold:     DefaultFileThreshold,
		MaxRecentMessages: 4,
	}
}

// Message represents a chat message for offloading decisions.
type Message struct {
	Role    string
	Content string
}

// OffloadResult describes how messages should be handled.
type OffloadResult struct {
	Mode           Mode
	InlineMessages []Message // messages to send inline
	FileContent    string    // content to upload as file (empty for inline mode)
	ContextNote    string    // note to prepend explaining offloaded context
}

// Offloader decides how to handle conversation context.
type Offloader struct {
	cfg Config
}

// New creates an Offloader with the given config.
func New(cfg Config) *Offloader {
	if cfg.HybridThreshold <= 0 {
		cfg.HybridThreshold = DefaultHybridThreshold
	}
	if cfg.FileThreshold <= 0 {
		cfg.FileThreshold = DefaultFileThreshold
	}
	if cfg.MaxRecentMessages <= 0 {
		cfg.MaxRecentMessages = 4
	}
	return &Offloader{cfg: cfg}
}

// EstimateLength estimates the total character count of messages.
func EstimateLength(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += utf8.RuneCountInString(m.Role) + utf8.RuneCountInString(m.Content) + 10
	}
	return total
}

// DetectMode determines the offloading mode based on prompt length.
func (o *Offloader) DetectMode(messages []Message) Mode {
	length := EstimateLength(messages)
	if length >= o.cfg.FileThreshold {
		return File
	}
	if length >= o.cfg.HybridThreshold {
		return Hybrid
	}
	return Inline
}

// Process analyzes messages and returns the offload decision.
func (o *Offloader) Process(messages []Message) OffloadResult {
	mode := o.DetectMode(messages)

	switch mode {
	case Inline:
		return OffloadResult{
			Mode:           Inline,
			InlineMessages: messages,
		}
	case Hybrid:
		return o.hybridSplit(messages)
	case File:
		return o.fileSplit(messages)
	}
	return OffloadResult{Mode: Inline, InlineMessages: messages}
}

func (o *Offloader) hybridSplit(messages []Message) OffloadResult {
	if len(messages) <= o.cfg.MaxRecentMessages {
		return OffloadResult{Mode: Inline, InlineMessages: messages}
	}

	splitIdx := len(messages) - o.cfg.MaxRecentMessages
	oldMessages := messages[:splitIdx]
	recentMessages := messages[splitIdx:]

	fileContent := renderMessagesForFile(oldMessages)
	note := fmt.Sprintf("[SYSTEM NOTE: %d earlier messages have been summarized in an attached file for context. The most recent messages follow.]", len(oldMessages))

	// Inject context note as system message
	inline := make([]Message, 0, len(recentMessages)+1)
	inline = append(inline, Message{Role: "system", Content: note})
	inline = append(inline, recentMessages...)

	return OffloadResult{
		Mode:           Hybrid,
		InlineMessages: inline,
		FileContent:    fileContent,
		ContextNote:    note,
	}
}

func (o *Offloader) fileSplit(messages []Message) OffloadResult {
	if len(messages) == 0 {
		return OffloadResult{Mode: Inline, InlineMessages: messages}
	}

	// Keep only the latest user message inline
	lastUser := messages[len(messages)-1]
	historyMessages := messages[:len(messages)-1]

	fileContent := renderMessagesForFile(historyMessages)
	note := fmt.Sprintf("[SYSTEM NOTE: Full conversation history (%d messages) has been attached as a file. Only the latest message is shown inline.]", len(historyMessages))

	inline := []Message{
		{Role: "system", Content: note},
		lastUser,
	}

	return OffloadResult{
		Mode:           File,
		InlineMessages: inline,
		FileContent:    fileContent,
		ContextNote:    note,
	}
}

func renderMessagesForFile(messages []Message) string {
	var sb strings.Builder
	sb.WriteString("# Conversation History\n\n")
	for i, m := range messages {
		sb.WriteString(fmt.Sprintf("## Message %d [%s]\n\n", i+1, m.Role))
		sb.WriteString(m.Content)
		sb.WriteString("\n\n---\n\n")
	}
	return sb.String()
}
