package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/keaume34/qwen2api/internal/config"
	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/promptcache"
	"github.com/keaume34/qwen2api/internal/qwen"
	"github.com/keaume34/qwen2api/internal/session"
	"github.com/keaume34/qwen2api/internal/toolcall"
)

func (h *handlers) responses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		_ = r.Body.Close()
	}()

	bodyBytes, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "read body: "+readErr.Error())
		return
	}
	if dumpPath := os.Getenv("QWEN2API_DEBUG_RESPONSES_DUMP"); dumpPath != "" {
		_ = os.WriteFile(dumpPath, append(bodyBytes, '\n'), 0644)
	}
	var req openai.ResponsesRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	// Convert Responses API input to ChatMessages
	messages, err := h.convertResponsesInput(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if len(messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "input must not be empty")
		return
	}

	// Auto-compact: if messages exceed context window threshold, compact them
	if h.deps.Config.Session.ContextWindowTokens > 0 {
		contextWindow := session.GetContextWindow(req.Model, h.deps.Config.Session.ContextWindowTokens)
		compactResult := session.AutoCompact(
			messages,
			contextWindow,
			h.deps.Config.Session.CompactThreshold,
			h.deps.TokenCounter,
		)
		if compactResult.WasCompacted {
			h.deps.Logger.Info("responses: auto-compact triggered",
				"model", req.Model,
				"context_window", contextWindow,
				"original_tokens", compactResult.OriginalLen,
				"compact_tokens", compactResult.CompactLen,
				"messages_before", len(messages),
				"messages_after", len(compactResult.Compacted),
			)
			messages = compactResult.Compacted
		}
	}

	// Build equivalent ChatRequest
	chatReq := openai.ChatRequest{
		Model:       req.Model,
		Messages:    messages,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}
	if req.MaxOutputTokens != nil {
		chatReq.MaxTokens = req.MaxOutputTokens
	}

	// Convert ResponseTools to OpenAI Tools
	for _, rt := range req.Tools {
		if rt.Type == "function" {
			chatReq.Tools = append(chatReq.Tools, openai.Tool{
				Type: "function",
				Function: openai.ToolFunction{
					Name:        rt.Name,
					Description: rt.Description,
					Parameters:  rt.Parameters,
				},
			})
		}
	}

	chatReq.Model = h.deps.Config.ResolveModel(chatReq.Model)
	upstreamReq := buildQwenRequestFull(chatReq, h.deps.Config.Features.Multimodal, h.deps.Config.Features.ThinkingMode)

	// Detect inline `data:` image URIs so we can upload them to Qwen OSS on
	// the first attempt (once a token is in hand).
	needImageUpload := false
	if h.deps.Config.Features.Multimodal && h.imageUploader != nil {
		for _, m := range chatReq.Messages {
			for _, p := range m.Parts() {
				if strings.HasPrefix(p.ImageRef(), "data:") {
					needImageUpload = true
					break
				}
			}
			if needImageUpload {
				break
			}
		}
	}

	maxAttempts := 1
	if h.deps.Config.Features.RetryOnTokenFailure && h.deps.Config.Retry.MaxAttempts > 1 {
		maxAttempts = h.deps.Config.Retry.MaxAttempts
	}

	// Conversation continuity: reuse a previously-allocated chat_id when this
	// request is a follow-up turn in an existing conversation. This avoids the
	// /api/v2/chats/new round-trip and (more importantly) lets qwen.ai keep its
	// server-side context cache warm across turns.
	//
	// Three signals are checked, in order of decreasing precision:
	//   1. `previous_response_id` (codex CLI sends this on follow-up turns).
	//      We stash chat_id under this key at the end of every successful turn,
	//      keyed by the OUTGOING response_id, so the next request hits.
	//   2. "Prior prefix hash" — hash of everything except the trailing
	//      [assistant, user] pair. When this turn is a follow-up, this hash
	//      equals what we stored at the previous turn (which stored the hash
	//      of its entire messages slice).
	//   3. First-message hash (system prompt). Catches "same agent role"
	//      cases even when conversation diverges.
	var cacheKey, lookupContinuityKey, prevRespKey string
	if h.deps.Config.Features.PromptCaching && h.deps.Cache != nil && len(upstreamReq.Messages) > 0 {
		cacheKey = promptcache.Key(upstreamReq.Model+":responses", upstreamReq.Messages[0].Content)
	}
	if h.deps.Config.Features.ConversationContinuity && h.deps.Cache != nil {
		if req.PreviousResponseID != "" {
			prevRespKey = promptcache.Key(upstreamReq.Model+":responses:prev", req.PreviousResponseID)
		}
		if k := lookupConvKey(upstreamReq.Model+":responses:conv", chatReq.Messages); k != "" {
			lookupContinuityKey = k
		}
	}
	// The STORE key always covers this request's full message slice (including
	// the trailing user we just received). On the next turn the client will
	// append the assistant reply + a new user, so its LOOKUP — which drops the
	// last [assistant, user] pair — will recover exactly this hash.
	var storeContinuityKey string
	if h.deps.Config.Features.ConversationContinuity && h.deps.Cache != nil && len(chatReq.Messages) > 0 {
		storeContinuityKey = promptcache.Key(upstreamReq.Model+":responses:conv", collapseMessages(chatReq.Messages))
	}

	var (
		token           config.Token
		body            io.ReadCloser
		retries         int
		cacheHit        bool
		activeChatID    string
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		t, err := h.deps.TokenPool.Take()
		if err != nil {
			h.metricsInc("qwen2api_no_upstream_token_total")
			writeError(w, http.StatusServiceUnavailable, "no_upstream_token", "no Qwen token configured")
			h.logRequestEndpoint(r, chatReq, "responses", "", http.StatusServiceUnavailable, time.Since(start), false, retries, err)
			return
		}
		token = t

		// Upload inline data-URI images to Qwen OSS on the first attempt.
		// Invalidate cache keys built from the pre-upload prompt because the
		// resolved URLs change the upstream content array's hash.
		if attempt == 1 && needImageUpload {
			if err := h.uploadDataURIsInPlace(r.Context(), token.Value, chatReq.Messages); err != nil {
				h.deps.Logger.Warn("responses: upload data-uri images failed; continuing without them", "err", err)
				h.metricsInc("qwen2api_image_upload_failed_total")
			}
			upstreamReq = buildQwenRequestFull(chatReq, h.deps.Config.Features.Multimodal, h.deps.Config.Features.ThinkingMode)
			cacheKey = ""
			lookupContinuityKey = ""
			storeContinuityKey = ""
			prevRespKey = ""
			needImageUpload = false
		}

		var chatID string
		if prevRespKey != "" {
			if cached, ok := h.deps.Cache.Get(prevRespKey); ok {
				chatID = cached
				cacheHit = true
				h.metricsInc("qwen2api_previous_response_hits_total")
			}
		}
		if chatID == "" && lookupContinuityKey != "" {
			if cached, ok := h.deps.Cache.Get(lookupContinuityKey); ok {
				chatID = cached
				cacheHit = true
				h.metricsInc("qwen2api_continuity_hits_total")
			}
		}
		if chatID == "" && cacheKey != "" {
			if cached, ok := h.deps.Cache.Get(cacheKey); ok {
				chatID = cached
				cacheHit = true
				h.metricsInc("qwen2api_cache_hits_total")
			} else {
				h.metricsInc("qwen2api_cache_misses_total")
			}
		}

		if chatID == "" {
			newID, err := h.deps.Qwen.NewChat(r.Context(), token.Value, upstreamReq.Model, upstreamReq.ChatType)
			if err != nil {
				if shouldRetry(err) && attempt < maxAttempts {
					retries++
					h.markBadAndLog(token.Value, err, "create chat session (responses)", attempt)
					continue
				}
				h.handleUpstreamFailure(w, token.Value, err, "create chat session")
				h.logRequestEndpoint(r, chatReq, "responses", token.Value, http.StatusBadGateway, time.Since(start), false, retries, err)
				return
			}
			chatID = newID
			if cacheKey != "" {
				h.deps.Cache.Put(cacheKey, chatID)
			}
		}
		// Always (cache-hit or freshly-allocated) record chat_id under the
		// store-key derived from this request's *current* message slice so the
		// next turn's prefix-hash lookup finds it.
		if storeContinuityKey != "" {
			h.deps.Cache.Put(storeContinuityKey, chatID)
		}
		upstreamReq.ChatID = chatID

		var cmpErr error
		body, cmpErr = h.deps.Qwen.Completions(r.Context(), token.Value, upstreamReq)
		if cmpErr != nil {
			if cacheHit {
				if prevRespKey != "" {
					h.deps.Cache.Invalidate(prevRespKey)
				}
				if lookupContinuityKey != "" {
					h.deps.Cache.Invalidate(lookupContinuityKey)
				}
				if storeContinuityKey != "" {
					h.deps.Cache.Invalidate(storeContinuityKey)
				}
				if cacheKey != "" {
					h.deps.Cache.Invalidate(cacheKey)
				}
				cacheHit = false
			}
			if shouldRetry(cmpErr) && attempt < maxAttempts {
				retries++
				h.markBadAndLog(token.Value, cmpErr, "open completion stream (responses)", attempt)
				continue
			}
			h.handleUpstreamFailure(w, token.Value, cmpErr, "open completion stream")
			h.logRequestEndpoint(r, chatReq, "responses", token.Value, http.StatusBadGateway, time.Since(start), false, retries, cmpErr)
			return
		}

		// chat.qwen.ai returns an empty stream on chat_id reuse. Detect
		// and retry with a fresh chat_id before the client sees anything.
		if cacheHit {
			replay, isEmpty, dbg := preflightChatIDReuseDebug(body)
			h.deps.Logger.Debug("responses: preflight cache reuse check", "chat_id", chatID, "model", upstreamReq.Model, "empty", isEmpty, "debug", dbg)
			if isEmpty {
				h.metricsInc("qwen2api_cache_empty_reuse_total")
				h.deps.Logger.Warn("responses: cache hit produced empty upstream stream; invalidating and retrying", "chat_id", chatID, "model", upstreamReq.Model, "preflight", dbg)
				if prevRespKey != "" {
					h.deps.Cache.Invalidate(prevRespKey)
				}
				if lookupContinuityKey != "" {
					h.deps.Cache.Invalidate(lookupContinuityKey)
				}
				if storeContinuityKey != "" {
					h.deps.Cache.Invalidate(storeContinuityKey)
				}
				if cacheKey != "" {
					h.deps.Cache.Invalidate(cacheKey)
				}
				cacheHit = false
				body = nil
				retries++
				if attempt >= maxAttempts {
					maxAttempts = attempt + 1
				}
				continue
			}
			body = replay
		}

		activeChatID = chatID
		break
	}

	if body == nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "all retries exhausted")
		h.logRequestEndpoint(r, chatReq, "responses", token.Value, http.StatusBadGateway, time.Since(start), false, retries, fmt.Errorf("all retries exhausted"))
		return
	}

	responseID := "resp_" + uuid.NewString()
	createdAt := unixNow()
	hasTools := len(req.Tools) > 0
	if !hasTools {
		// Proactively check if there are any tool calls/results in history
		for _, m := range messages {
			if m.Role == "tool" || len(m.ToolCalls) > 0 {
				hasTools = true
				break
			}
		}
	}

	// Stash chat_id under the response_id we're about to emit so the client's
	// next call carrying previous_response_id=<responseID> reuses this same
	// Qwen chat session (and its server-side context cache).
	if h.deps.Config.Features.ConversationContinuity && h.deps.Cache != nil && activeChatID != "" {
		h.deps.Cache.Put(promptcache.Key(upstreamReq.Model+":responses:prev", responseID), activeChatID)
	}

	if req.Stream {
		defer func() { _ = body.Close() }()
		h.proxyResponsesStream(r.Context(), w, body, responseID, createdAt, req.Model, hasTools, h.deps.Config.Features.MultiFormatToolParsing)
		h.metricsObserve("qwen2api_request_duration_seconds", time.Since(start).Seconds())
		h.logRequestEndpoint(r, chatReq, "responses", token.Value, http.StatusOK, time.Since(start), cacheHit, retries, nil)
		return
	}

	resp, _, _, err := h.collectResponsesCompletion(body, responseID, createdAt, req.Model, hasTools)
	if err != nil {
		h.handleUpstreamFailure(w, token.Value, err, "read completion stream (responses)")
		h.logRequestEndpoint(r, chatReq, "responses", token.Value, http.StatusBadGateway, time.Since(start), false, retries, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
	h.metricsObserve("qwen2api_request_duration_seconds", time.Since(start).Seconds())
	h.logRequestEndpoint(r, chatReq, "responses", token.Value, http.StatusOK, time.Since(start), cacheHit, retries, nil)
}

// convertResponsesInput converts Responses API input to ChatMessages.
func (h *handlers) convertResponsesInput(req openai.ResponsesRequest) ([]openai.ChatMessage, error) {
	if len(req.Input) == 0 {
		return nil, fmt.Errorf("input is required")
	}

	// Try string first
	var textInput string
	if err := json.Unmarshal(req.Input, &textInput); err == nil {
		msgs := []openai.ChatMessage{{
			Role:    "user",
			Content: jsonStringRaw(textInput),
		}}
		if req.Instructions != "" {
			msgs = append([]openai.ChatMessage{{
				Role:    "system",
				Content: jsonStringRaw(req.Instructions),
			}}, msgs...)
		}
		return msgs, nil
	}

	// Try array of input items
	var items []openai.ResponseInputItem
	if err := json.Unmarshal(req.Input, &items); err != nil {
		return nil, fmt.Errorf("input must be a string or array of input items: %w", err)
	}

	var msgs []openai.ChatMessage
	if req.Instructions != "" {
		msgs = append(msgs, openai.ChatMessage{
			Role:    "system",
			Content: jsonStringRaw(req.Instructions),
		})
	}

	for _, item := range items {
		switch item.Type {
		case "message":
			role := item.Role
			switch role {
			case "":
				role = "user"
			case "developer":
				// Responses API "developer" role is OpenAI's higher-priority
				// system equivalent (used by codex for permissions / skill
				// catalogues). Collapse to "system" for upstream Qwen.
				role = "system"
			}
			msgs = append(msgs, openai.ChatMessage{
				Role:    role,
				Content: item.Content,
			})
		case "function_call":
			// Past assistant tool call echoed back as part of conversation
			// history. Reconstruct as an assistant message carrying a
			// tool_calls entry so collapseMessages renders it consistently.
			msgs = append(msgs, openai.ChatMessage{
				Role:    "assistant",
				Content: jsonStringRaw(""),
				ToolCalls: []openai.ToolCall{{
					ID:   item.CallID,
					Type: "function",
					Function: openai.ToolCallFunction{
						Name:      item.Name,
						Arguments: item.Arguments,
					},
				}},
			})
		case "function_call_output":
			msgs = append(msgs, openai.ChatMessage{
				Role:       "tool",
				Content:    jsonStringRaw(item.Output),
				ToolCallID: item.CallID,
			})
		case "reasoning":
			// Reasoning summaries from a prior turn — skip; we already
			// stripped <think> blocks at the gateway boundary and there is
			// no benefit to feeding the model back its own private thoughts.
			continue
		default:
			// Treat unknown types as user messages if they have content
			if len(item.Content) > 0 {
				msgs = append(msgs, openai.ChatMessage{
					Role:    "user",
					Content: item.Content,
				})
			}
		}
	}

	return msgs, nil
}

// collectResponsesCompletion reads the upstream stream and builds a ResponseObject.
func (h *handlers) collectResponsesCompletion(body io.ReadCloser, id string, created int64, model string, hasTools bool) (openai.ResponseObject, string, bool, error) {
	defer func() { _ = body.Close() }()
	reader := qwen.NewStreamReader(body)
	var content strings.Builder
	var thinkingContent strings.Builder
	inThinking := false

	for {
		evt, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return openai.ResponseObject{}, "", false, err
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			continue
		}
		choice := evt.Delta.Choices[0]
		phase := choice.Delta.Phase
		rawContent := choice.Delta.Content

		// Separate thinking from content
		if phase == "think" {
			if !inThinking {
				inThinking = true
			}
			thinkingContent.WriteString(rawContent)
			continue
		}
		if inThinking && (phase == "answer" || phase == "") {
			inThinking = false
		}
		content.WriteString(rawContent)
	}

	fullContent := content.String()
	fullThinking := thinkingContent.String()
	truncated := hasTools && isTruncatedToolCallContent(fullContent)

	var output []openai.ResponseOutputItem

	// Add reasoning item first if thinking was present
	if fullThinking != "" {
		output = append(output, openai.ResponseOutputItem{
			Type:   "reasoning",
			ID:     "rs_" + uuid.NewString(),
			Status: "completed",
			Summary: []openai.ResponseContentBlock{{
				Type: "summary_text",
				Text: fullThinking,
			}},
		})
	}

	if hasTools {
		result := toolcall.ParseWithFormats(fullContent, h.deps.Config.Features.MultiFormatToolParsing)
		if len(result.ToolCalls) > 0 {
			if strings.TrimSpace(result.Content) != "" {
				output = append(output, openai.ResponseOutputItem{
					Type:   "message",
					ID:     "msg_" + uuid.NewString(),
					Status: "completed",
					Role:   "assistant",
					Content: []openai.ResponseContentBlock{{
						Type: "output_text",
						Text: result.Content,
					}},
				})
			}
			for _, tc := range result.ToolCalls {
				output = append(output, openai.ResponseOutputItem{
					Type:      "function_call",
					ID:        "fc_" + uuid.NewString(),
					Status:    "completed",
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
			return openai.ResponseObject{
				ID:        id,
				Object:    "response",
				CreatedAt: created,
				Status:    "completed",
				Model:     model,
				Output:    output,
			}, fullContent, truncated, nil
		}
	}

	output = append(output, openai.ResponseOutputItem{
		Type:   "message",
		ID:     "msg_" + uuid.NewString(),
		Status: "completed",
		Role:   "assistant",
		Content: []openai.ResponseContentBlock{{
			Type: "output_text",
			Text: fullContent,
		}},
	})

	return openai.ResponseObject{
		ID:        id,
		Object:    "response",
		CreatedAt: created,
		Status:    "completed",
		Model:     model,
		Output:    output,
	}, fullContent, truncated, nil
}

// proxyResponsesStream emits SSE events in the Responses API streaming format.
func (h *handlers) proxyResponsesStream(ctx context.Context, w http.ResponseWriter, body io.Reader, id string, created int64, model string, hasTools, multiFormat bool) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	seq := 0
	emitEvent := func(eventType string, data openai.ResponseStreamEvent) {
		data.SequenceNumber = seq
		seq++
		raw, err := json.Marshal(data)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, raw)
		flush()
	}

	// Emit response.created
	emitEvent("response.created", openai.ResponseStreamEvent{
		Type: "response.created",
		Response: &openai.ResponseObject{
			ID:        id,
			Object:    "response",
			CreatedAt: created,
			Status:    "in_progress",
			Model:     model,
			Output:    []openai.ResponseOutputItem{},
		},
	})

	// Emit response.in_progress
	emitEvent("response.in_progress", openai.ResponseStreamEvent{
		Type: "response.in_progress",
		Response: &openai.ResponseObject{
			ID:        id,
			Object:    "response",
			CreatedAt: created,
			Status:    "in_progress",
			Model:     model,
			Output:    []openai.ResponseOutputItem{},
		},
	})

	// Emit response.output_item.added for the message.
	// IMPORTANT: codex CLI requires the `content` array to be present (even
	// if empty) so that the item passes its `ResponseItem::Message`
	// deserialization and the active message id gets registered. Without
	// this, every subsequent output_text.delta is logged as "OutputTextDelta
	// without active item". We bypass ResponseStreamEvent here so the empty
	// `content: []` slice survives JSON marshalling instead of being dropped
	// by `omitempty`.
	emitRaw := func(eventType string, payload any) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, raw)
		flush()
	}

	reader := qwen.NewStreamReader(body)
	inThinking := false
	thinkingStarted := false
	thinkingDone := false
	reasoningID := "rs_" + uuid.NewString()
	var fullThinking strings.Builder
	var fullContent strings.Builder
	outputIdx := 0 // tracks current output_index
	msgID := ""

	// Start a reasoning item if thinking begins
	startReasoningItem := func() {
		if thinkingStarted {
			return
		}
		thinkingStarted = true
		reasoningID = "rs_" + uuid.NewString()
		emitRaw("response.output_item.added", map[string]any{
			"type":            "response.output_item.added",
			"sequence_number": seq,
			"output_index":    outputIdx,
			"item": map[string]any{
				"type":    "reasoning",
				"id":      reasoningID,
				"status":  "in_progress",
				"summary": []any{},
			},
		})
		seq++
	}

	// Close reasoning item
	closeReasoningItem := func() {
		if !thinkingStarted || thinkingDone {
			return
		}
		thinkingDone = true
		emitRaw("response.output_item.done", map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": seq,
			"output_index":    outputIdx,
			"item": map[string]any{
				"type":    "reasoning",
				"id":      reasoningID,
				"status":  "completed",
				"summary": []map[string]any{{"type": "summary_text", "text": fullThinking.String()}},
			},
		})
		seq++
		outputIdx++
	}

	// Start message item (after reasoning if present)
	startMessageItem := func() {
		if msgID != "" {
			return
		}
		closeReasoningItem()
		msgID = "msg_" + uuid.NewString()
		emitRaw("response.output_item.added", map[string]any{
			"type":            "response.output_item.added",
			"sequence_number": seq,
			"output_index":    outputIdx,
			"item": map[string]any{
				"type":    "message",
				"id":      msgID,
				"status":  "in_progress",
				"role":    "assistant",
				"content": []any{},
			},
		})
		seq++
	}

	contentIdx := 0
	// When hasTools is true the model may emit <tool_call>...</tool_call>
	// blocks inline with text. We must NOT forward those blocks to the client
	// as text deltas — codex / claude-code render them as code blocks even
	// though we also emit them as proper function_call output items, which
	// gives the user a confusing duplicate display. So when we suspect a tool
	// call is being formed, we hold back any unflushed bytes until the end of
	// the stream and then emit only the cleaned text portion. `emittedLen`
	// tracks bytes of `fullContent` already flushed as deltas.
	emittedLen := 0
	triggered := false

	// Longest tag prefix we have to look out for. Hold back at least this
	// many trailing bytes from delta emission when no tag has been confirmed
	// yet, so we don't accidentally flush "<tool_ca" and only realise it was
	// part of a tool call on the next chunk.
	holdback := len("<tool_call")
	if multiFormat && len("<function_calls") > holdback {
		holdback = len("<function_calls")
	}

	emitTextDelta := func(s string) {
		if s == "" {
			return
		}
		startMessageItem()
		emitEvent("response.output_text.delta", openai.ResponseStreamEvent{
			Type:         "response.output_text.delta",
			ItemID:       msgID,
			OutputIndex:  outputIdx,
			ContentIndex: contentIdx,
			Delta:        s,
		})
	}

	keepAlive := EffectiveKeepAlive(h.deps.Config.StreamKeepAliveSeconds)
	pacer := newStreamPacer(reader, keepAlive)
	loopErr := pacer.loop(ctx,
		func(evt qwen.StreamEvent) error {
			if evt.Done {
				return nil
			}
			if evt.Comment != "" {
				writeSSEKeepAlive(w, flusher)
				return nil
			}
			if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
				return nil
			}
			choice := evt.Delta.Choices[0]
			phase := choice.Delta.Phase
			rawContent := choice.Delta.Content

			// Handle thinking phase — emit as reasoning item
			if phase == "think" {
				if !inThinking {
					inThinking = true
					startReasoningItem()
				}
				fullThinking.WriteString(rawContent)
				return nil
			}
			// Transition from thinking to answer
			if inThinking && (phase == "answer" || phase == "") {
				inThinking = false
			}

			text := rawContent
			if text == "" {
				return nil
			}
			fullContent.WriteString(text)

			if !hasTools {
				// No tool support: stream every byte as soon as it arrives.
				emitTextDelta(text)
				emittedLen = fullContent.Len()
				return nil
			}

			if triggered {
				// Already inside / past a suspected tool call. Buffer everything
				// and decide at the end of the stream.
				return nil
			}

			accumulated := fullContent.String()
			if strings.Contains(accumulated, "<tool_call") ||
				(multiFormat && strings.Contains(accumulated, "<function_calls")) {
				triggered = true
				return nil
			}

			// Hold back the longest possible tag prefix so we don't leak a
			// partial "<tool_ca" to the client. Align to the start of a rune so
			// we never split UTF-8 sequences mid-codepoint.
			safe := len(accumulated) - holdback
			if safe <= emittedLen {
				return nil
			}
			for safe > emittedLen && !utf8.RuneStart(accumulated[safe]) {
				safe--
			}
			if safe > emittedLen {
				emitTextDelta(accumulated[emittedLen:safe])
				emittedLen = safe
			}
			return nil
		},
		func() { writeSSEKeepAlive(w, flusher) },
	)
	truncated := false
	if loopErr != nil && !errors.Is(loopErr, context.Canceled) {
		h.deps.Logger.Warn("responses stream read error", "err", loopErr)
		truncated = true
	}
	if errors.Is(loopErr, context.Canceled) {
		return
	}

	// Close reasoning if still open
	if inThinking || thinkingStarted {
		closeReasoningItem()
		inThinking = false
	}

	accumulated := fullContent.String()

	// Decide whether the assistant produced tool calls or plain text. We
	// always need to close the active message item (text/done events) when
	// there's text content — `hasTools` only tells us the caller is
	// expecting that tool calls *may* appear, not that they will.
	var toolCalls []openai.ToolCall
	textContent := accumulated
	if hasTools {
		result := toolcall.ParseWithFormats(accumulated, multiFormat)
		if len(result.ToolCalls) > 0 {
			toolCalls = result.ToolCalls
			textContent = result.Content
		}
	}

	// Flush any text we held back that turned out to be plain prose (no tool
	// calls in it, or text after / between the stripped tool_call blocks).
	if len(textContent) > emittedLen {
		unsent := textContent[emittedLen:]
		emitTextDelta(unsent)
	}

	// Close out the message item. We do this even when tool calls follow so
	// codex / claude-code can register the assistant turn properly.
	startMessageItem() // ensure message item was started
	emitEvent("response.output_text.done", openai.ResponseStreamEvent{
		Type:         "response.output_text.done",
		OutputIndex:  outputIdx,
		ContentIndex: 0,
		ItemID:       msgID,
		Text:         textContent,
	})
	emitEvent("response.content_part.done", openai.ResponseStreamEvent{
		Type:         "response.content_part.done",
		OutputIndex:  outputIdx,
		ContentIndex: 0,
		ItemID:       msgID,
		Part: &openai.ResponseContentBlock{
			Type: "output_text",
			Text: textContent,
		},
	})
	emitRaw("response.output_item.done", map[string]any{
		"type":            "response.output_item.done",
		"sequence_number": seq,
		"output_index":    outputIdx,
		"item": map[string]any{
			"type":    "message",
			"id":      msgID,
			"status":  "completed",
			"role":    "assistant",
			"content": []map[string]any{{"type": "output_text", "text": textContent}},
		},
	})
	seq++
	outputIdx++

	// Emit function_call items after the message.
	for _, tc := range toolCalls {
		fcID := "fc_" + uuid.NewString()
		emitEvent("response.output_item.added", openai.ResponseStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: outputIdx,
			Item: &openai.ResponseOutputItem{
				Type:      "function_call",
				ID:        fcID,
				Status:    "in_progress",
				CallID:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: "",
			},
		})
		emitEvent("response.function_call_arguments.delta", openai.ResponseStreamEvent{
			Type:        "response.function_call_arguments.delta",
			OutputIndex: outputIdx,
			ItemID:      fcID,
			Delta:       tc.Function.Arguments,
		})
		emitEvent("response.function_call_arguments.done", openai.ResponseStreamEvent{
			Type:        "response.function_call_arguments.done",
			OutputIndex: outputIdx,
			ItemID:      fcID,
			Arguments:   tc.Function.Arguments,
		})
		emitEvent("response.output_item.done", openai.ResponseStreamEvent{
			Type:        "response.output_item.done",
			OutputIndex: outputIdx,
			Item: &openai.ResponseOutputItem{
				Type:      "function_call",
				ID:        fcID,
				Status:    "completed",
				CallID:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
		outputIdx++
	}

	// Final output array mirrors what was emitted as items.
	var finalOutput []openai.ResponseOutputItem
	// Include reasoning item if present
	if fullThinking.Len() > 0 {
		finalOutput = append(finalOutput, openai.ResponseOutputItem{
			Type:   "reasoning",
			ID:     reasoningID,
			Status: "completed",
			Summary: []openai.ResponseContentBlock{{
				Type: "summary_text",
				Text: fullThinking.String(),
			}},
		})
	}
	finalOutput = append(finalOutput, openai.ResponseOutputItem{
		Type:   "message",
		ID:     msgID,
		Status: "completed",
		Role:   "assistant",
		Content: []openai.ResponseContentBlock{{
			Type: "output_text",
			Text: textContent,
		}},
	})
	for _, tc := range toolCalls {
		finalOutput = append(finalOutput, openai.ResponseOutputItem{
			Type:      "function_call",
			ID:        "fc_" + uuid.NewString(),
			Status:    "completed",
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	finalStatus := "completed"
	if truncated {
		// Upstream stream cut mid-response — tell the client so it
		// can choose to retry or surface a clearer error instead of
		// treating partial output as a clean completion.
		finalStatus = "incomplete"
	}
	emitEvent("response.completed", openai.ResponseStreamEvent{
		Type: "response.completed",
		Response: &openai.ResponseObject{
			ID:        id,
			Object:    "response",
			CreatedAt: created,
			Status:    finalStatus,
			Model:     model,
			Output:    finalOutput,
		},
	})

	fmt.Fprint(w, "data: [DONE]\n\n")
	flush()
}
