package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/keaume34/qwen2api/internal/claude"
	"github.com/keaume34/qwen2api/internal/config"
	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/qwen"
	"github.com/keaume34/qwen2api/internal/toolcall"
)

func (h *handlers) claudeMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		_ = r.Body.Close()
	}()

	var req claude.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
		return
	}

	if req.Model == "" {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if len(req.Messages) == 0 {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "messages must not be empty")
		return
	}
	// Be lenient about max_tokens: some Claude clients (e.g. claude-code) may omit
	// it. Default to a sensible upper bound rather than rejecting the request.
	if req.MaxTokens == 0 {
		req.MaxTokens = 8192
	}

	// Convert Claude request to OpenAI format
	oaiReq, err := claude.ToOpenAI(req)
	if err != nil {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "failed to convert request: "+err.Error())
		return
	}

	// Resolve model alias
	oaiReq.Model = h.deps.Config.ResolveModel(oaiReq.Model)

	// Build upstream request
	upstreamReq := buildQwenRequestFull(oaiReq, h.deps.Config.Features.Multimodal, h.deps.Config.Features.ThinkingMode)

	maxAttempts := 1
	if h.deps.Config.Features.RetryOnTokenFailure && h.deps.Config.Retry.MaxAttempts > 1 {
		maxAttempts = h.deps.Config.Retry.MaxAttempts
	}

	var (
		token   config.Token
		body    io.ReadCloser
		retries int
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		t, takeErr := h.deps.TokenPool.Take()
		if takeErr != nil {
			writeClaudeError(w, http.StatusServiceUnavailable, "overloaded_error", "no Qwen token available")
			h.logRequestEndpoint(r, oaiReq, "claude", "", http.StatusServiceUnavailable, time.Since(start), false, retries, takeErr)
			return
		}
		token = t

		chatID, chatErr := h.deps.Qwen.NewChat(r.Context(), token.Value, upstreamReq.Model, upstreamReq.ChatType)
		if chatErr != nil {
			if shouldRetry(chatErr) && attempt < maxAttempts {
				retries++
				h.markBadAndLog(token.Value, chatErr, "create chat session (claude)", attempt)
				continue
			}
			writeClaudeError(w, http.StatusBadGateway, "api_error", "failed to create chat: "+chatErr.Error())
			h.logRequestEndpoint(r, oaiReq, "claude", token.Value, http.StatusBadGateway, time.Since(start), false, retries, chatErr)
			return
		}
		upstreamReq.ChatID = chatID

		var cmpErr error
		body, cmpErr = h.deps.Qwen.Completions(r.Context(), token.Value, upstreamReq)
		if cmpErr != nil {
			if shouldRetry(cmpErr) && attempt < maxAttempts {
				retries++
				h.markBadAndLog(token.Value, cmpErr, "open completion stream (claude)", attempt)
				continue
			}
			writeClaudeError(w, http.StatusBadGateway, "api_error", "upstream request failed: "+cmpErr.Error())
			h.logRequestEndpoint(r, oaiReq, "claude", token.Value, http.StatusBadGateway, time.Since(start), false, retries, cmpErr)
			return
		}
		break
	}

	if body == nil {
		writeClaudeError(w, http.StatusBadGateway, "api_error", "all retries exhausted")
		h.logRequestEndpoint(r, oaiReq, "claude", token.Value, http.StatusBadGateway, time.Since(start), false, retries, fmt.Errorf("all retries exhausted"))
		return
	}
	defer func() { _ = body.Close() }()

	hasTools := len(req.Tools) > 0
	oaiReq.Stream = req.Stream
	if hasTools && len(oaiReq.Tools) == 0 {
		oaiReq.Tools = make([]openai.Tool, len(req.Tools))
	}
	msgID := "msg_" + uuid.NewString()
	if req.Stream {
		h.streamClaudeResponse(w, body, req.Model, msgID, hasTools, h.deps.Config.Features.MultiFormatToolParsing)
	} else {
		h.aggregateClaudeResponse(w, body, req.Model, msgID, hasTools, h.deps.Config.Features.MultiFormatToolParsing)
	}
	h.metricsObserve("qwen2api_request_duration_seconds", time.Since(start).Seconds())
	h.logRequestEndpoint(r, oaiReq, "claude", token.Value, http.StatusOK, time.Since(start), false, retries, nil)
}

// streamClaudeResponse reads raw Qwen SSE, applies thinking wrapping and tool
// detection, and emits a well-formed Claude Messages SSE stream.
func (h *handlers) streamClaudeResponse(w http.ResponseWriter, body io.ReadCloser, model, msgID string, hasTools, multiFormat bool) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	emit := func(eventType string, payload claude.StreamEvent) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, raw)
		flush()
	}

	// 1) message_start with the model the client requested
	msgStart := &claude.MessagesResponse{
		ID:      msgID,
		Type:    "message",
		Role:    "assistant",
		Content: []claude.ContentPart{},
		Model:   model,
		Usage:   claude.Usage{},
	}
	emit("message_start", claude.StreamEvent{Type: "message_start", Message: msgStart})

	// Track state.
	reader := qwen.NewStreamReader(body)
	inThinking := false
	textStarted := false
	var fullContent strings.Builder
	var emittedLen int
	triggered := false // true once we suspect a <tool_call> tag and buffer
	var inputTokens, outputTokens int

	startTextBlock := func() {
		if textStarted {
			return
		}
		textStarted = true
		emit("content_block_start", claude.StreamEvent{
			Type:         "content_block_start",
			Index:        intPtrLocal(0),
			ContentBlock: &claude.ContentPart{Type: "text", Text: ""},
		})
	}

	emitTextDelta := func(s string) {
		if s == "" {
			return
		}
		startTextBlock()
		emit("content_block_delta", claude.StreamEvent{
			Type:  "content_block_delta",
			Index: intPtrLocal(0),
			Delta: &claude.ContentDelta{Type: "text_delta", Text: s},
		})
	}

	stopReason := "end_turn"

	for {
		evt, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.deps.Logger.Warn("claude stream read error", "err", err)
			break
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			if evt.Delta != nil && evt.Delta.Usage != nil {
				if evt.Delta.Usage.InputTokens > 0 {
					inputTokens = evt.Delta.Usage.InputTokens
				}
				if evt.Delta.Usage.OutputTokens > 0 {
					outputTokens = evt.Delta.Usage.OutputTokens
				}
			}
			continue
		}
		choice := evt.Delta.Choices[0]
		text, next := wrapThinking(choice.Delta.Content, choice.Delta.Phase, inThinking)
		inThinking = next
		if text != "" {
			fullContent.WriteString(text)
		}

		if hasTools && !triggered {
			accumulated := fullContent.String()
			if strings.Contains(accumulated, "<tool_call") {
				triggered = true
			} else {
				// Keep a small look-behind so we don't split a multi-byte rune
				// or a partial <tool_call tag.
				safe := len(accumulated) - len("<tool_call")
				for safe > emittedLen && !utf8.RuneStart(accumulated[safe]) {
					safe--
				}
				if safe > emittedLen {
					chunkStr := accumulated[emittedLen:safe]
					emitTextDelta(chunkStr)
					emittedLen = safe
				}
			}
		} else if !hasTools {
			// No tool support — just stream text deltas directly.
			emitTextDelta(text)
			emittedLen = fullContent.Len()
		}

		if choice.FinishReason != nil {
			switch *choice.FinishReason {
			case "stop":
				stopReason = "end_turn"
			case "length":
				stopReason = "max_tokens"
			case "tool_calls":
				stopReason = "tool_use"
			}
		}
	}

	// If we ended while still in a thinking phase, close the tag.
	if inThinking {
		fullContent.WriteString("</think>")
	}

	accumulated := fullContent.String()
	// Estimate input/output tokens cheaply if upstream didn't supply them.
	if outputTokens == 0 {
		outputTokens = approxTokens(accumulated)
	}

	if hasTools {
		result := toolcall.ParseWithFormats(accumulated, multiFormat)
		if len(result.ToolCalls) > 0 {
			stopReason = "tool_use"
			cleanText := strings.TrimSpace(result.Content)
			// Emit any leading text content as a single block.
			if cleanText != "" {
				startTextBlock()
				// Only send the unsent portion as a single delta.
				if cleanText != accumulated[:min(len(accumulated), emittedLen)] {
					unsent := cleanText
					if emittedLen > 0 && emittedLen < len(cleanText) {
						unsent = cleanText[emittedLen:]
					}
					if unsent != "" {
						emit("content_block_delta", claude.StreamEvent{
							Type:  "content_block_delta",
							Index: intPtrLocal(0),
							Delta: &claude.ContentDelta{Type: "text_delta", Text: unsent},
						})
					}
				}
				emit("content_block_stop", claude.StreamEvent{
					Type:  "content_block_stop",
					Index: intPtrLocal(0),
				})
				textStarted = false // already closed
			} else if textStarted {
				emit("content_block_stop", claude.StreamEvent{
					Type:  "content_block_stop",
					Index: intPtrLocal(0),
				})
				textStarted = false
			}

			// Emit each tool_use as its own block.
			toolStartIdx := 0
			if cleanText != "" {
				toolStartIdx = 1
			}
			for i, tc := range result.ToolCalls {
				idx := toolStartIdx + i
				emit("content_block_start", claude.StreamEvent{
					Type:  "content_block_start",
					Index: intPtrLocal(idx),
					ContentBlock: &claude.ContentPart{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: json.RawMessage("{}"),
					},
				})
				args := tc.Function.Arguments
				if args == "" {
					args = "{}"
				}
				emit("content_block_delta", claude.StreamEvent{
					Type:  "content_block_delta",
					Index: intPtrLocal(idx),
					Delta: &claude.ContentDelta{Type: "input_json_delta", PartialJSON: args},
				})
				emit("content_block_stop", claude.StreamEvent{
					Type:  "content_block_stop",
					Index: intPtrLocal(idx),
				})
			}
		} else {
			// No tool calls detected after all — flush any buffered text.
			if len(accumulated) > emittedLen {
				emitTextDelta(accumulated[emittedLen:])
				emittedLen = len(accumulated)
			}
			if textStarted {
				emit("content_block_stop", claude.StreamEvent{
					Type:  "content_block_stop",
					Index: intPtrLocal(0),
				})
				textStarted = false
			}
		}
	} else {
		if textStarted {
			emit("content_block_stop", claude.StreamEvent{
				Type:  "content_block_stop",
				Index: intPtrLocal(0),
			})
			textStarted = false
		}
	}

	// Always close out with message_delta + message_stop so claude-code doesn't hang.
	emit("message_delta", claude.StreamEvent{
		Type:   "message_delta",
		Delta2: &claude.MessageDelta{StopReason: stopReason},
		Usage:  &claude.Usage{InputTokens: inputTokens, OutputTokens: outputTokens},
	})
	emit("message_stop", claude.StreamEvent{Type: "message_stop"})
}

// aggregateClaudeResponse reads the full Qwen stream and returns a single
// Claude MessagesResponse JSON envelope.
func (h *handlers) aggregateClaudeResponse(w http.ResponseWriter, body io.ReadCloser, model, msgID string, hasTools, multiFormat bool) {
	reader := qwen.NewStreamReader(body)
	var content strings.Builder
	inThinking := false
	finishReason := ""
	var inputTokens, outputTokens int

	for {
		evt, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.deps.Logger.Warn("claude aggregate read error", "err", err)
			break
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil {
			continue
		}
		if evt.Delta.Usage != nil {
			if evt.Delta.Usage.InputTokens > 0 {
				inputTokens = evt.Delta.Usage.InputTokens
			}
			if evt.Delta.Usage.OutputTokens > 0 {
				outputTokens = evt.Delta.Usage.OutputTokens
			}
		}
		if len(evt.Delta.Choices) == 0 {
			continue
		}
		choice := evt.Delta.Choices[0]
		text, next := wrapThinking(choice.Delta.Content, choice.Delta.Phase, inThinking)
		inThinking = next
		content.WriteString(text)
		if choice.FinishReason != nil {
			finishReason = *choice.FinishReason
		}
	}
	if inThinking {
		content.WriteString("</think>")
	}

	full := content.String()
	stopReason := "end_turn"
	switch finishReason {
	case "length":
		stopReason = "max_tokens"
	case "tool_calls":
		stopReason = "tool_use"
	}

	var blocks []claude.ContentPart
	if hasTools {
		result := toolcall.ParseWithFormats(full, multiFormat)
		if len(result.ToolCalls) > 0 {
			stopReason = "tool_use"
			if strings.TrimSpace(result.Content) != "" {
				blocks = append(blocks, claude.ContentPart{Type: "text", Text: result.Content})
			}
			for _, tc := range result.ToolCalls {
				args := tc.Function.Arguments
				if args == "" {
					args = "{}"
				}
				blocks = append(blocks, claude.ContentPart{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(args),
				})
			}
		}
	}
	if len(blocks) == 0 {
		// Always include at least one text block so Claude clients don't choke.
		blocks = []claude.ContentPart{{Type: "text", Text: full}}
	}

	if outputTokens == 0 {
		outputTokens = approxTokens(full)
	}

	resp := claude.MessagesResponse{
		ID:         msgID,
		Type:       "message",
		Role:       "assistant",
		Content:    blocks,
		Model:      model,
		StopReason: stopReason,
		Usage:      claude.Usage{InputTokens: inputTokens, OutputTokens: outputTokens},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeClaudeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(claude.ErrorResponse{
		Type: "error",
		Error: claude.ErrorBody{
			Type:    errType,
			Message: message,
		},
	})
}

// intPtrLocal returns a pointer to i. Local copy to avoid exporting from the
// claude package.
func intPtrLocal(i int) *int { return &i }

// approxTokens estimates token count for a string as a rough 4 chars/token.
func approxTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n == 0 {
		return 1
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

