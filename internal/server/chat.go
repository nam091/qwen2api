package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/keaume34/qwen2api/internal/affinity"
	"github.com/keaume34/qwen2api/internal/config"
	"github.com/keaume34/qwen2api/internal/filecache"
	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/promptcache"
	"github.com/keaume34/qwen2api/internal/qwen"
	"github.com/keaume34/qwen2api/internal/reqlog"
	"github.com/keaume34/qwen2api/internal/toolcall"
	"github.com/keaume34/qwen2api/internal/topicisolation"
)

func (h *handlers) chatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		_ = r.Body.Close()
	}()
	var req openai.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "messages must not be empty")
		return
	}

	apiKey := bearerOrQuery(r)

	// File Cache (Phase 1 features)
	h.processFileCache(apiKey, req.Messages)

	// Topic Isolation (Phase 1 features)
	req.Messages = h.applyTopicIsolation(req.Messages)

	req.Model = h.deps.Config.ResolveModel(req.Model)
	upstreamReq := buildQwenRequestWithOptions(req, h.deps.Config.Features.Multimodal)

	maxAttempts := 1
	if h.deps.Config.Features.RetryOnTokenFailure && h.deps.Config.Retry.MaxAttempts > 1 {
		maxAttempts = h.deps.Config.Retry.MaxAttempts
	}
	continuationAttempts := 1
	if h.deps.Config.Retry.MaxAttempts > 1 {
		continuationAttempts = h.deps.Config.Retry.MaxAttempts
	}

	var (
		token      config.Token
		body       io.ReadCloser
		lastUp     *qwen.UpstreamError
		lastTr     error
		retries    int
		cacheHit   bool
		cacheKey   string
	)

	// Session Affinity (Phase 1 features)
	var sessionKey string
	if h.deps.Config.Features.SessionAffinity && h.deps.Affinity != nil {
		var firstUserText string
		for _, m := range req.Messages {
			if strings.ToLower(m.Role) == "user" {
				firstUserText = m.Text()
				break
			}
		}
		if firstUserText != "" {
			sessionKey = affinity.DeriveSessionKey(apiKey, firstUserText)
		}
	}

	if h.deps.Config.Features.PromptCaching && h.deps.Cache != nil {
		cacheKey = promptcache.Key(upstreamReq.Model, upstreamReq.Messages[0].Content)
	}

	// Conversation continuity: hash by all messages except the last user turn,
	// so follow-ups in the same conversation reuse the chat session.
	var continuityKey string
	if h.deps.Config.Features.ConversationContinuity && h.deps.Cache != nil && len(req.Messages) >= 2 {
		prefix := collapseMessages(req.Messages[:len(req.Messages)-1])
		continuityKey = promptcache.Key(upstreamReq.Model+":conv", prefix)
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var chatID string
		var t config.Token
		var err error

		useAffinity := false
		if attempt == 1 && sessionKey != "" {
			if rec, ok := h.deps.Affinity.Lookup(sessionKey); ok {
				t = config.Token{Value: rec.TokenValue}
				chatID = rec.ChatID
				useAffinity = true
			}
		}

		if !useAffinity {
			t, err = h.deps.TokenPool.Take()
			if err != nil {
				h.metricsInc("qwen2api_no_upstream_token_total")
				writeError(w, http.StatusServiceUnavailable, "no_upstream_token", "no Qwen token configured; set QWEN2API_TOKENS")
				return
			}
		}
		token = t

		if chatID == "" && cacheKey != "" {
			if cached, ok := h.deps.Cache.Get(cacheKey); ok {
				chatID = cached
				cacheHit = true
				h.metricsInc("qwen2api_cache_hits_total")
			} else {
				h.metricsInc("qwen2api_cache_misses_total")
			}
		}
		if chatID == "" && continuityKey != "" {
			if cached, ok := h.deps.Cache.Get(continuityKey); ok {
				chatID = cached
				cacheHit = true
				h.metricsInc("qwen2api_continuity_hits_total")
			}
		}

		if chatID == "" {
			chatID, err = h.deps.Qwen.NewChat(r.Context(), token.Value, upstreamReq.Model, upstreamReq.ChatType)
			if err != nil {
				if shouldRetry(err) && attempt < maxAttempts {
					retries++
					h.markBadAndLog(token.Value, err, "create chat session", attempt)
					lastUp, lastTr = asUpstreamErr(err), err
					continue
				}
				h.handleUpstreamFailure(w, token.Value, err, "create chat session")
				h.logRequest(r, req, token.Value, http.StatusBadGateway, time.Since(start), cacheHit, retries, err)
				return
			}
			if cacheKey != "" {
				h.deps.Cache.Put(cacheKey, chatID)
			}
			if continuityKey != "" {
				h.deps.Cache.Put(continuityKey, chatID)
			}
		}
		upstreamReq.ChatID = chatID

		body, err = h.deps.Qwen.Completions(r.Context(), token.Value, upstreamReq)
		if err != nil {
			if cacheHit && cacheKey != "" {
				h.deps.Cache.Invalidate(cacheKey)
				cacheHit = false
			}
			if shouldRetry(err) && attempt < maxAttempts {
				retries++
				h.markBadAndLog(token.Value, err, "open completion stream", attempt)
				lastUp, lastTr = asUpstreamErr(err), err
				continue
			}
			h.handleUpstreamFailure(w, token.Value, err, "open completion stream")
			h.logRequest(r, req, token.Value, http.StatusBadGateway, time.Since(start), cacheHit, retries, err)
			return
		}

		// Bind session affinity on success
		if sessionKey != "" && chatID != "" {
			h.deps.Affinity.Bind(sessionKey, token.Value, chatID)
		}
		break
	}

	if body == nil {
		var msg string
		if lastUp != nil {
			msg = fmt.Sprintf("all retries exhausted; last upstream status: %d", lastUp.Status)
		} else if lastTr != nil {
			msg = "all retries exhausted: " + lastTr.Error()
		} else {
			msg = "all retries exhausted"
		}
		writeError(w, http.StatusBadGateway, "upstream_error", msg)
		h.logRequest(r, req, token.Value, http.StatusBadGateway, time.Since(start), cacheHit, retries, errors.New(msg))
		return
	}
	completionID := "chatcmpl-" + uuid.NewString()
	created := unixNow()
	hasTools := len(req.Tools) > 0
	if req.Stream {
		defer func() {
			_ = body.Close()
		}()
		h.proxyStream(w, body, completionID, created, req.Model, hasTools)
		h.metricsObserve("qwen2api_request_duration_seconds", time.Since(start).Seconds())
		h.logRequest(r, req, token.Value, http.StatusOK, time.Since(start), cacheHit, retries, nil)
		return
	}

	resp, fullContent, truncated, err := h.collectChatCompletion(body, completionID, created, req.Model, hasTools)
	if err != nil {
		h.handleUpstreamFailure(w, token.Value, err, "read completion stream")
		h.logRequest(r, req, token.Value, http.StatusBadGateway, time.Since(start), cacheHit, retries, err)
		return
	}
	if hasTools && truncated {
		for attempt := 2; attempt <= continuationAttempts && truncated; attempt++ {
			retries++
			contReq := buildContinuationRequest(req, fullContent)
			contUpstreamReq := buildQwenRequestWithOptions(contReq, h.deps.Config.Features.Multimodal)
			contUpstreamReq.ChatID = upstreamReq.ChatID
			contBody, contErr := h.deps.Qwen.Completions(r.Context(), token.Value, contUpstreamReq)
			if contErr != nil {
				h.handleUpstreamFailure(w, token.Value, contErr, "open continuation stream")
				h.logRequest(r, req, token.Value, http.StatusBadGateway, time.Since(start), cacheHit, retries, contErr)
				return
			}
			resp, fullContent, truncated, err = h.collectChatCompletion(contBody, completionID, created, req.Model, hasTools)
			if err != nil {
				h.handleUpstreamFailure(w, token.Value, err, "read continuation stream")
				h.logRequest(r, req, token.Value, http.StatusBadGateway, time.Since(start), cacheHit, retries, err)
				return
			}
		}
		if truncated {
			writeError(w, http.StatusBadGateway, "upstream_error", "tool call response appears truncated after retries")
			h.logRequest(r, req, token.Value, http.StatusBadGateway, time.Since(start), cacheHit, retries, errors.New("truncated tool call after retries"))
			return
		}
	}

	writeJSON(w, http.StatusOK, resp)
	h.metricsObserve("qwen2api_request_duration_seconds", time.Since(start).Seconds())
	h.logRequest(r, req, token.Value, http.StatusOK, time.Since(start), cacheHit, retries, nil)
}

func (h *handlers) metricsInc(name string) {
	if h.deps.Metrics != nil {
		h.deps.Metrics.Counter(name).Inc()
	}
}

func (h *handlers) metricsObserve(name string, v float64) {
	if h.deps.Metrics != nil {
		h.deps.Metrics.Histogram(name).Observe(v)
	}
}

func (h *handlers) logRequest(r *http.Request, req openai.ChatRequest, token string, status int, latency time.Duration, cacheHit bool, retries int, err error) {
	if h.deps.ReqLog == nil {
		return
	}
	entry := reqlog.Entry{
		RequestID: r.Header.Get("X-Request-Id"),
		APIKey:    reqlog.MaskKey(bearerOrQuery(r)),
		Model:     req.Model,
		Token:     reqlog.MaskKey(token),
		Status:    status,
		Latency:   latency.Milliseconds(),
		Stream:    req.Stream,
		HasTools:  len(req.Tools) > 0,
		CacheHit:  cacheHit,
		Retries:   retries,
	}
	if err != nil {
		entry.Error = err.Error()
	}
	h.deps.ReqLog.Log(entry, h.deps.Config.Features.RequestLogging)
}

// shouldRetry returns true if the error indicates a token problem worth retrying with another token.
func shouldRetry(err error) bool {
	var ue *qwen.UpstreamError
	if errors.As(err, &ue) {
		return ue.Status == http.StatusUnauthorized || ue.Status == http.StatusForbidden || ue.Status == http.StatusTooManyRequests
	}
	return false
}

func asUpstreamErr(err error) *qwen.UpstreamError {
	var ue *qwen.UpstreamError
	if errors.As(err, &ue) {
		return ue
	}
	return nil
}

func (h *handlers) markBadAndLog(token string, err error, action string, attempt int) {
	h.deps.TokenPool.MarkBad(token)
	if ue := asUpstreamErr(err); ue != nil {
		h.deps.Logger.Warn("retrying with new token", "action", action, "attempt", attempt, "status", ue.Status)
	} else {
		h.deps.Logger.Warn("retrying with new token", "action", action, "attempt", attempt, "err", err)
	}
}

func (h *handlers) handleUpstreamFailure(w http.ResponseWriter, token string, err error, action string) {
	var upstream *qwen.UpstreamError
	if errors.As(err, &upstream) {
		h.deps.Logger.Warn("upstream error", "action", action, "status", upstream.Status, "body", truncate(upstream.Body, 256))
		if upstream.Status == http.StatusUnauthorized || upstream.Status == http.StatusForbidden {
			h.deps.TokenPool.MarkBad(token)
		}
		writeError(w, http.StatusBadGateway, "upstream_error", fmt.Sprintf("qwen %s failed: %d", action, upstream.Status))
		return
	}
	h.deps.Logger.Error("transport error", "action", action, "err", err)
	writeError(w, http.StatusBadGateway, "upstream_error", "transport error: "+err.Error())
}

func buildQwenRequest(req openai.ChatRequest) qwen.CompletionRequest {
	return buildQwenRequestWithOptions(req, true)
}

func buildQwenRequestWithOptions(req openai.ChatRequest, multimodalEnabled bool) qwen.CompletionRequest {
	chatType := chatTypeFromModel(req.Model)
	thinkingEnabled := false
	if req.EnableThinking != nil {
		thinkingEnabled = *req.EnableThinking
	} else if strings.HasSuffix(req.Model, "-thinking") || strings.Contains(req.Model, "thinking") {
		thinkingEnabled = true
	}

	// Upstream chat.qwen.ai only accepts a single user-role message per
	// request (it is the web-client API, not a multi-turn API). When the
	// caller sends multiple messages or non-user roles (system/assistant),
	// collapse the whole history into one user message with role-prefixed
	// content so the conversation context is preserved.
	collapsed := collapseMessages(req.Messages)

	if len(req.Tools) > 0 {
		toolPrompt := toolcall.FormatToolsPrompt(req.Tools)
		collapsed = toolPrompt + "\n\n" + collapsed
	}

	extra := map[string]interface{}{}
	if multimodalEnabled {
		var allImages []string
		for _, m := range req.Messages {
			allImages = append(allImages, m.Images()...)
		}
		if len(allImages) > 0 {
			extra["files"] = imageFilesPayload(allImages)
			var imageNote strings.Builder
			imageNote.WriteString("\n\n[Attached images]\n")
			for i, url := range allImages {
				fmt.Fprintf(&imageNote, "%d. %s\n", i+1, url)
			}
			collapsed += imageNote.String()
		}
	}

	msgs := []qwen.Message{{
		Role:     "user",
		Content:  collapsed,
		ChatType: chatType,
		Extra:    extra,
		FeatureConfig: &qwen.FeatureConfig{
			OutputSchema:    "phase",
			ThinkingEnabled: thinkingEnabled,
		},
	}}

	return qwen.CompletionRequest{
		ChatType:    chatType,
		SubChatType: chatType,
		ChatMode:    "normal",
		Model:       baseModelID(req.Model),
		Messages:    msgs,
		SessionID:   uuid.NewString(),
		ID:          uuid.NewString(),
	}
}

// imageFilesPayload renders image URLs into the structure Qwen's web client uses.
func imageFilesPayload(urls []string) []map[string]any {
	out := make([]map[string]any, 0, len(urls))
	for _, u := range urls {
		out = append(out, map[string]any{
			"type": "image",
			"url":  u,
		})
	}
	return out
}

// collapseMessages folds a multi-turn OpenAI-style history into a single
// flat user prompt. Each turn is rendered as `Role: text` and joined with
// blank lines. A single user message with no system context passes through
// untouched so simple requests stay byte-for-byte the same upstream.
func collapseMessages(msgs []openai.ChatMessage) string {
	if len(msgs) == 1 && strings.EqualFold(msgs[0].Role, "user") {
		return msgs[0].Text()
	}
	var b strings.Builder
	wrote := false
	for _, m := range msgs {
		text := strings.TrimSpace(m.Text())
		if text == "" && len(m.ToolCalls) == 0 {
			continue
		}
		var label string
		switch strings.ToLower(m.Role) {
		case "system":
			label = "System"
		case "assistant":
			label = "Assistant"
		case "tool", "function":
			label = "Tool"
			if m.ToolCallID != "" {
				label = fmt.Sprintf("Tool [call_id=%s]", m.ToolCallID)
			}
		default:
			label = "User"
		}
		if wrote {
			b.WriteString("\n\n")
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(text)
		if strings.ToLower(m.Role) == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				b.WriteString("\n<tool_call>\n")
				b.WriteString(fmt.Sprintf(`{"name": "%s", "arguments": %s}`, tc.Function.Name, tc.Function.Arguments))
				b.WriteString("\n</tool_call>")
			}
		}
		wrote = true
	}
	// Nudge the model to continue as the assistant after a trailing user turn.
	if last := lastNonEmptyRole(msgs); strings.EqualFold(last, "user") {
		b.WriteString("\n\nAssistant:")
	}
	return b.String()
}

func lastNonEmptyRole(msgs []openai.ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.TrimSpace(msgs[i].Text()) != "" {
			return msgs[i].Role
		}
	}
	return ""
}

func chatTypeFromModel(model string) string {
	switch {
	case strings.Contains(model, "search"):
		return "search"
	case strings.Contains(model, "image"):
		return "t2i"
	case strings.Contains(model, "video"):
		return "t2v"
	default:
		return "t2t"
	}
}

// baseModelID strips qwen2api-specific suffixes (e.g. `-thinking`, `-search`)
// before forwarding to the upstream model field.
func baseModelID(model string) string {
	suffixes := []string{"-thinking-search", "-image-edit", "-deep-research", "-thinking", "-search", "-video", "-image"}
	for _, s := range suffixes {
		if strings.HasSuffix(model, s) {
			return strings.TrimSuffix(model, s)
		}
	}
	return model
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func strPtr(s string) *string { return &s }

func jsonStringRaw(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func buildContinuationRequest(req openai.ChatRequest, partialAssistant string) openai.ChatRequest {
	out := req
	out.Messages = append([]openai.ChatMessage{}, req.Messages...)
	if strings.TrimSpace(partialAssistant) != "" {
		out.Messages = append(out.Messages, openai.ChatMessage{
			Role:    "assistant",
			Content: jsonStringRaw(partialAssistant),
		})
	}
	out.Messages = append(out.Messages, openai.ChatMessage{
		Role:    "user",
		Content: jsonStringRaw("Continue from exactly where you stopped. If you were emitting a <tool_call>, output only the completed <tool_call> block(s)."),
	})
	return out
}

func isTruncatedToolCallContent(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "<tool_call") && !strings.Contains(trimmed, "</tool_call>") {
		return true
	}
	if strings.HasSuffix(trimmed, "<tool_call") || strings.HasSuffix(trimmed, "<tool_call>") {
		return true
	}
	return false
}

func (h *handlers) collectChatCompletion(body io.ReadCloser, id string, created int64, model string, hasTools bool) (openai.ChatCompletion, string, bool, error) {
	defer func() {
		_ = body.Close()
	}()
	reader := qwen.NewStreamReader(body)
	var content strings.Builder
	inThinking := false
	finishReason := "stop"

	for {
		evt, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return openai.ChatCompletion{}, "", false, err
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
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

	fullContent := content.String()
	truncated := hasTools && isTruncatedToolCallContent(fullContent)
	if hasTools {
		result := toolcall.ParseWithFormats(fullContent, h.deps.Config.Features.MultiFormatToolParsing)
		if len(result.ToolCalls) > 0 {
			var contentPtr *string
			if strings.TrimSpace(result.Content) != "" {
				contentPtr = strPtr(result.Content)
			}
			resp := openai.ChatCompletion{
				ID:      id,
				Object:  "chat.completion",
				Created: created,
				Model:   model,
				Choices: []openai.Choice{{
					Index: 0,
					Message: openai.ChatMessageOut{
						Role:      "assistant",
						Content:   contentPtr,
						ToolCalls: result.ToolCalls,
					},
					FinishReason: "tool_calls",
				}},
			}
			return resp, fullContent, truncated, nil
		}
	}

	resp := openai.ChatCompletion{
		ID:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []openai.Choice{{
			Index:        0,
			Message:      openai.ChatMessageOut{Role: "assistant", Content: strPtr(fullContent)},
			FinishReason: finishReason,
		}},
	}
	return resp, fullContent, truncated, nil
}

// proxyStream re-emits upstream events as OpenAI-style SSE chunks.
func (h *handlers) proxyStream(w http.ResponseWriter, body io.Reader, id string, created int64, model string, hasTools bool) {
	if hasTools {
		h.proxyStreamWithToolDetection(w, body, id, created, model)
		return
	}
	h.proxyStreamDirect(w, body, id, created, model)
}

// proxyStreamDirect is the original streaming path with no tool detection.
func (h *handlers) proxyStreamDirect(w http.ResponseWriter, body io.Reader, id string, created int64, model string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	reader := qwen.NewStreamReader(body)
	roleSent := false
	inThinking := false

	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	emit := func(chunk openai.StreamChunk) {
		raw, err := json.Marshal(chunk)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", raw)
		flush()
	}

	for {
		evt, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.deps.Logger.Warn("stream read error", "err", err)
			break
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			continue
		}
		choice := evt.Delta.Choices[0]
		role := ""
		if !roleSent {
			role = "assistant"
			roleSent = true
		}
		text := choice.Delta.Content
		text, inThinking = wrapThinking(text, choice.Delta.Phase, inThinking)
		if text == "" && role == "" && choice.FinishReason == nil {
			continue
		}
		chunk := openai.StreamChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []openai.StreamChoice{{
				Index:        0,
				Delta:        openai.Delta{Role: role, Content: text},
				FinishReason: choice.FinishReason,
			}},
		}
		emit(chunk)
	}

	if inThinking {
		emit(openai.StreamChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []openai.StreamChoice{{Delta: openai.Delta{Content: "</think>"}}},
		})
	}

	stop := "stop"
	emit(openai.StreamChunk{
		ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []openai.StreamChoice{{Index: 0, Delta: openai.Delta{}, FinishReason: &stop}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	flush()
}

// proxyStreamWithToolDetection buffers the stream to detect <tool_call> blocks.
// Content before the first <tool_call> is streamed normally. Once detected,
// the remainder is buffered and tool calls are emitted as structured deltas.
func (h *handlers) proxyStreamWithToolDetection(w http.ResponseWriter, body io.Reader, id string, created int64, model string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	reader := qwen.NewStreamReader(body)
	inThinking := false
	var fullContent strings.Builder
	var emittedLen int
	triggered := false
	roleSent := false

	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	emit := func(chunk openai.StreamChunk) {
		raw, err := json.Marshal(chunk)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", raw)
		flush()
	}

	for {
		evt, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.deps.Logger.Warn("stream read error", "err", err)
			break
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			continue
		}
		choice := evt.Delta.Choices[0]
		text, next := wrapThinking(choice.Delta.Content, choice.Delta.Phase, inThinking)
		inThinking = next
		fullContent.WriteString(text)

		if !triggered {
			accumulated := fullContent.String()
			if strings.Contains(accumulated, "<tool_call") {
				triggered = true
				continue
			}
			// Stream content that is safe (far enough from a potential tag start)
			safe := len(accumulated) - len("<tool_call")
			// Ensure we don't cut in the middle of a multi-byte UTF-8 character
			for safe > emittedLen && !utf8.RuneStart(accumulated[safe]) {
				safe--
			}
			if safe > emittedLen {
				chunk := accumulated[emittedLen:safe]
				role := ""
				if !roleSent {
					role = "assistant"
					roleSent = true
				}
				emit(openai.StreamChunk{
					ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
					Choices: []openai.StreamChoice{{
						Index: 0,
						Delta: openai.Delta{Role: role, Content: chunk},
					}},
				})
				emittedLen = safe
			}
		}
	}

	if inThinking {
		fullContent.WriteString("</think>")
	}

	accumulated := fullContent.String()
	result := toolcall.ParseWithFormats(accumulated, h.deps.Config.Features.MultiFormatToolParsing)

	if len(result.ToolCalls) > 0 {
		// Emit any remaining content before tool calls that wasn't streamed yet
		remainingContent := strings.TrimSpace(result.Content)
		if len(remainingContent) > emittedLen {
			unsent := remainingContent[emittedLen:]
			if unsent != "" {
				role := ""
				if !roleSent {
					role = "assistant"
					roleSent = true
				}
				emit(openai.StreamChunk{
					ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
					Choices: []openai.StreamChoice{{
						Index: 0,
						Delta: openai.Delta{Role: role, Content: unsent},
					}},
				})
			}
		}

		// Emit tool calls as structured deltas
		for i, tc := range result.ToolCalls {
			role := ""
			if !roleSent {
				role = "assistant"
				roleSent = true
			}
			emit(openai.StreamChunk{
				ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
				Choices: []openai.StreamChoice{{
					Index: 0,
					Delta: openai.Delta{
						Role: role,
						ToolCalls: []openai.ToolCallDelta{{
							Index: i,
							ID:    tc.ID,
							Type:  "function",
							Function: openai.ToolCallFuncDelta{
								Name:      tc.Function.Name,
								Arguments: tc.Function.Arguments,
							},
						}},
					},
				}},
			})
		}

		toolCalls := "tool_calls"
		emit(openai.StreamChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []openai.StreamChoice{{Index: 0, Delta: openai.Delta{}, FinishReason: &toolCalls}},
		})
	} else {
		// No tool calls found — emit any remaining buffered content
		if len(accumulated) > emittedLen {
			unsent := accumulated[emittedLen:]
			if unsent != "" {
				role := ""
				if !roleSent {
					role = "assistant"
					roleSent = true
				}
				emit(openai.StreamChunk{
					ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
					Choices: []openai.StreamChoice{{
						Index: 0,
						Delta: openai.Delta{Role: role, Content: unsent},
					}},
				})
			}
		}

		stop := "stop"
		emit(openai.StreamChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []openai.StreamChoice{{Index: 0, Delta: openai.Delta{}, FinishReason: &stop}},
		})
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	flush()
}

// wrapThinking inserts <think>/</think> markers when upstream toggles phase
// between "think" and "answer". Returns the updated text and the new
// `inThinking` flag.
func wrapThinking(content, phase string, inThinking bool) (string, bool) {
	switch phase {
	case "think":
		if !inThinking {
			return "<think>" + content, true
		}
		return content, true
	case "answer", "":
		if inThinking {
			return "</think>" + content, false
		}
		return content, false
	default:
		return content, inThinking
	}
}

// aggregateStream consumes the upstream stream and returns a single
// chat.completion JSON envelope (for non-stream client requests).
func (h *handlers) aggregateStream(w http.ResponseWriter, body io.Reader, id string, created int64, model string, hasTools bool) {
	reader := qwen.NewStreamReader(body)
	var content strings.Builder
	inThinking := false
	finishReason := "stop"

	for {
		evt, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.deps.Logger.Warn("stream read error", "err", err)
			break
		}
		if evt.Done {
			break
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
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

	fullContent := content.String()

	if hasTools {
		result := toolcall.ParseWithFormats(fullContent, h.deps.Config.Features.MultiFormatToolParsing)
		if len(result.ToolCalls) > 0 {
			var contentPtr *string
			if strings.TrimSpace(result.Content) != "" {
				contentPtr = strPtr(result.Content)
			}
			resp := openai.ChatCompletion{
				ID:      id,
				Object:  "chat.completion",
				Created: created,
				Model:   model,
				Choices: []openai.Choice{{
					Index: 0,
					Message: openai.ChatMessageOut{
						Role:      "assistant",
						Content:   contentPtr,
						ToolCalls: result.ToolCalls,
					},
					FinishReason: "tool_calls",
				}},
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}

	resp := openai.ChatCompletion{
		ID:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []openai.Choice{{
			Index:        0,
			Message:      openai.ChatMessageOut{Role: "assistant", Content: strPtr(fullContent)},
			FinishReason: finishReason,
		}},
	}
	writeJSON(w, http.StatusOK, resp)
}

var fileBlockRe = regexp.MustCompile(`<file\s+path=["']?([^"'>\s]+)["']?>([\s\S]*?)</file>`)

func (h *handlers) processFileCache(apiKey string, msgs []openai.ChatMessage) {
	if h.deps.FileCache == nil || !h.deps.Config.Features.FileCache {
		return
	}
	for i, m := range msgs {
		text := m.Text()
		if text == "" {
			continue
		}
		modifiedText := fileBlockRe.ReplaceAllStringFunc(text, func(match string) string {
			submatches := fileBlockRe.FindStringSubmatch(match)
			if len(submatches) < 3 {
				return match
			}
			filePath := submatches[1]
			content := submatches[2]
			if filecache.IsUnchangedHint(content) {
				if cached, ok := h.deps.FileCache.Get(apiKey, filePath); ok {
					return fmt.Sprintf(`<file path="%s">%s</file>`, filePath, cached)
				}
			} else {
				h.deps.FileCache.Put(apiKey, filePath, content)
			}
			return match
		})
		if modifiedText != text {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(modifiedText); err == nil {
				msgs[i].Content = bytes.TrimSpace(buf.Bytes())
			}
		}
	}
}

func (h *handlers) applyTopicIsolation(msgs []openai.ChatMessage) []openai.ChatMessage {
	if !h.deps.Config.Features.TopicIsolation || len(msgs) < 3 {
		return msgs
	}
	var firstUserIdx = -1
	for i, m := range msgs {
		if strings.ToLower(m.Role) == "user" {
			firstUserIdx = i
			break
		}
	}
	if firstUserIdx == -1 {
		return msgs
	}
	var lastUserIdx = -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.ToLower(msgs[i].Role) == "user" {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx <= firstUserIdx {
		return msgs
	}

	firstText := msgs[firstUserIdx].Text()
	lastText := msgs[lastUserIdx].Text()

	detector := topicisolation.NewDetector(0.1)
	if detector.Changed(firstText, lastText) {
		if h.deps.Logger != nil {
			h.deps.Logger.Info("topic isolation triggered: user changed topic", "first", truncate(firstText, 50), "last", truncate(lastText, 50))
		}
		var newMsgs []openai.ChatMessage
		for _, m := range msgs {
			if strings.ToLower(m.Role) == "system" {
				newMsgs = append(newMsgs, m)
			}
		}
		newMsgs = append(newMsgs, msgs[lastUserIdx])
		return newMsgs
	}
	return msgs
}
