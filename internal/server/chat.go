package server

import (
	"bytes"
	"context"
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
	"github.com/keaume34/qwen2api/internal/session"
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

	// Auto-compact: if messages exceed context window threshold, compact them
	if h.deps.Config.Session.ContextWindowTokens > 0 {
		// Resolve context window per model, fallback to config default
		contextWindow := session.GetContextWindow(req.Model, h.deps.Config.Session.ContextWindowTokens)
		compactResult := session.AutoCompact(
			req.Messages,
			contextWindow,
			h.deps.Config.Session.CompactThreshold,
			h.deps.TokenCounter,
		)
		if compactResult.WasCompacted {
			h.deps.Logger.Info("auto-compact triggered",
				"model", req.Model,
				"context_window", contextWindow,
				"original_tokens", compactResult.OriginalLen,
				"compact_tokens", compactResult.CompactLen,
				"messages_before", len(req.Messages),
				"messages_after", len(compactResult.Compacted),
			)
			req.Messages = compactResult.Compacted
		}
	}

	req.Model = h.deps.Config.ResolveModel(req.Model)

	// Session persistence: resolve session by context hash
	var sessionHash string
	var sess *session.Session
	if h.deps.Config.Features.SessionPersistence && h.sessionStore != nil {
		sessionHash = session.HashMessages(req.Messages, req.Model)
		if sessionHash != "" {
			sess = h.sessionStore.ResolveByContextHash(sessionHash)
		}
	}

	upstreamReq := buildQwenRequestFull(req, h.deps.Config.Features.Multimodal, h.deps.Config.Features.ThinkingMode)

	// If session has a summary and enough history, prepend it to the collapsed text
	if sess != nil && sess.Summary != "" && len(req.Messages) > h.deps.Config.Session.RollingHistoryK {
		if len(upstreamReq.Messages) > 0 {
			prefix := "[Previous context summary]\n" + sess.Summary + "\n\n"
			if len(upstreamReq.Messages[0].ContentParts) > 0 {
				// Prepend to the text part
				for i := range upstreamReq.Messages[0].ContentParts {
					if upstreamReq.Messages[0].ContentParts[i].Type == "text" {
						upstreamReq.Messages[0].ContentParts[i].Text = prefix + upstreamReq.Messages[0].ContentParts[i].Text
						break
					}
				}
			} else {
				upstreamReq.Messages[0].Content = prefix + upstreamReq.Messages[0].Content
			}
		}
	}

	// Detect whether any message contains inline `data:` image URIs that need
	// to be uploaded to Qwen OSS before the upstream request can succeed.
	// Upload happens on the first attempt once a token is in hand.
	needImageUpload := false
	if h.deps.Config.Features.Multimodal && h.imageUploader != nil {
		for _, m := range req.Messages {
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

	// Conversation continuity: lookup is by hash of msgs[:-2] (drop the prior
	// assistant + the just-received user). It matches a STORE key written at
	// the previous turn that hashed that turn's FULL message slice.
	var lookupContinuityKey string
	if h.deps.Config.Features.ConversationContinuity && h.deps.Cache != nil {
		lookupContinuityKey = lookupConvKey(upstreamReq.Model+":conv", req.Messages)
	}
	var storeContinuityKey string
	if h.deps.Config.Features.ConversationContinuity && h.deps.Cache != nil && len(req.Messages) > 0 {
		storeContinuityKey = promptcache.Key(upstreamReq.Model+":conv", collapseMessages(req.Messages))
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
				h.logRequest(r, req, "", http.StatusServiceUnavailable, time.Since(start), false, 0, err)
				return
			}
		}
		token = t

		// Upload any inline data-URI images to Qwen OSS on the first attempt
		// only. Rebuild the upstream request so the resolved OSS URLs land
		// in the multimodal content array. We also invalidate prompt and
		// continuity cache keys we computed pre-upload because the prompt
		// hash changes once the data URIs are gone.
		if attempt == 1 && needImageUpload {
			if err := h.uploadDataURIsInPlace(r.Context(), token.Value, req.Messages); err != nil {
				h.deps.Logger.Warn("upload data-uri images failed; continuing without them", "err", err)
				h.metricsInc("qwen2api_image_upload_failed_total")
			}
			upstreamReq = buildQwenRequestFull(req, h.deps.Config.Features.Multimodal, h.deps.Config.Features.ThinkingMode)
			cacheKey = ""
			lookupContinuityKey = ""
			storeContinuityKey = ""
			needImageUpload = false
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
		if chatID == "" && lookupContinuityKey != "" {
			if cached, ok := h.deps.Cache.Get(lookupContinuityKey); ok {
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
		}
		if storeContinuityKey != "" {
			h.deps.Cache.Put(storeContinuityKey, chatID)
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

		// chat.qwen.ai returns an empty stream when the chat_id has
		// already been used for a completion. Detect that and retry with
		// a fresh chat_id before any bytes are sent to the client.
		if cacheHit {
			replay, isEmpty, dbg := preflightChatIDReuseDebug(body)
			h.deps.Logger.Debug("preflight cache reuse check", "chat_id", chatID, "model", upstreamReq.Model, "empty", isEmpty, "debug", dbg)
			if isEmpty {
				h.metricsInc("qwen2api_cache_empty_reuse_total")
				h.deps.Logger.Warn("cache hit produced empty upstream stream; invalidating and retrying", "chat_id", chatID, "model", upstreamReq.Model, "preflight", dbg)
				if cacheKey != "" {
					h.deps.Cache.Invalidate(cacheKey)
				}
				if lookupContinuityKey != "" {
					h.deps.Cache.Invalidate(lookupContinuityKey)
				}
				if storeContinuityKey != "" {
					h.deps.Cache.Invalidate(storeContinuityKey)
				}
				cacheHit = false
				chatID = ""
				body = nil
				// Force a fresh /chats/new + Completions on the next
				// iteration. Bump retries so dashboards reflect it.
				retries++
				if attempt >= maxAttempts {
					maxAttempts = attempt + 1
				}
				continue
			}
			body = replay
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
	if !hasTools {
		// Proactively check if there are any tool calls/results in history
		for _, m := range req.Messages {
			if m.Role == "tool" || len(m.ToolCalls) > 0 {
				hasTools = true
				break
			}
		}
	}
	// Estimate input tokens from request messages and tool definitions.
	// Qwen upstream never sends usage. System prompt is already in messages (role:"system").
	inputTokensFallback := estimateInputTokensOpenAI(req.Messages, req.Tools)

	if req.Stream {
		defer func() {
			_ = body.Close()
		}()
		// Build Continuer for truncation auto-continuation (only when tools present)
		var cont toolcall.Continuer
		if hasTools {
			cont = &qwenContinuer{
				client:  h.deps.Qwen,
				token:   token.Value,
				chatID:  upstreamReq.ChatID,
				baseReq: upstreamReq,
			}
		}
		h.proxyStream(r.Context(), w, body, completionID, created, req.Model, hasTools, inputTokensFallback, cont)
		h.metricsObserve("qwen2api_request_duration_seconds", time.Since(start).Seconds())
		h.logRequest(r, req, token.Value, http.StatusOK, time.Since(start), cacheHit, retries, nil)
		// Store session messages after streaming (we don't have the response text,
		// but we store the user messages for context tracking)
		if sess != nil && sessionHash != "" {
			h.storeSessionAndMaybeSummarize(sessionHash, req.Messages, "", token.Value)
		}
		return
	}

	resp, fullContent, truncated, err := h.collectChatCompletion(body, completionID, created, req.Model, hasTools, inputTokensFallback)
	if err != nil {
		h.handleUpstreamFailure(w, token.Value, err, "read completion stream")
		h.logRequest(r, req, token.Value, http.StatusBadGateway, time.Since(start), cacheHit, retries, err)
		return
	}
	// Store session messages after non-streaming response
	if sess != nil && sessionHash != "" {
		h.storeSessionAndMaybeSummarize(sessionHash, req.Messages, fullContent, token.Value)
	}
	if hasTools && truncated {
		for attempt := 2; attempt <= continuationAttempts && truncated; attempt++ {
			retries++
			contReq := buildContinuationRequest(req, fullContent)
			contUpstreamReq := buildQwenRequestFull(contReq, h.deps.Config.Features.Multimodal, h.deps.Config.Features.ThinkingMode)
			contUpstreamReq.ChatID = upstreamReq.ChatID
			contBody, contErr := h.deps.Qwen.Completions(r.Context(), token.Value, contUpstreamReq)
			if contErr != nil {
				h.handleUpstreamFailure(w, token.Value, contErr, "open continuation stream")
				h.logRequest(r, req, token.Value, http.StatusBadGateway, time.Since(start), cacheHit, retries, contErr)
				return
			}
			resp, fullContent, truncated, err = h.collectChatCompletion(contBody, completionID, created, req.Model, hasTools, inputTokensFallback)
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
	h.logRequestEndpoint(r, req, "chat", token, status, latency, cacheHit, retries, err)
}

// logRequestEndpoint is like logRequest but also captures which logical
// endpoint (chat/claude/responses) handled the request, so the dashboard
// live log can distinguish them.
func (h *handlers) logRequestEndpoint(r *http.Request, req openai.ChatRequest, endpoint, token string, status int, latency time.Duration, cacheHit bool, retries int, err error) {
	if h.deps.ReqLog == nil {
		return
	}
	clientType := DetectClient(r)
	entry := reqlog.Entry{
		RequestID:  r.Header.Get("X-Request-Id"),
		APIKey:     reqlog.MaskKey(bearerOrQuery(r)),
		Endpoint:   endpoint,
		Path:       r.URL.Path,
		Model:      req.Model,
		Token:      reqlog.MaskKey(token),
		Status:     status,
		Latency:    latency.Milliseconds(),
		Stream:     req.Stream,
		HasTools:   len(req.Tools) > 0,
		CacheHit:   cacheHit,
		Retries:    retries,
		ClientType: string(clientType),
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
	return buildQwenRequestFull(req, multimodalEnabled, "auto")
}

// buildQwenRequestFull is the canonical builder; thinkingMode is "auto"/"on"/"off".
func buildQwenRequestFull(req openai.ChatRequest, multimodalEnabled bool, thinkingMode string) qwen.CompletionRequest {
	chatType := chatTypeFromModel(req.Model)
	thinkingEnabled := false
	switch strings.ToLower(strings.TrimSpace(thinkingMode)) {
	case "on", "always", "force_on", "true":
		thinkingEnabled = true
	case "off", "never", "force_off", "false":
		thinkingEnabled = false
	default: // "auto" / unset
		if req.EnableThinking != nil {
			thinkingEnabled = *req.EnableThinking
		} else if strings.HasSuffix(req.Model, "-thinking") || strings.Contains(req.Model, "thinking") {
			thinkingEnabled = true
		}
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
	var contentParts []qwen.ContentPart
	if multimodalEnabled {
		var allImages []string
		for _, m := range req.Messages {
			allImages = append(allImages, m.Images()...)
		}
		// Drop data: URIs that the handler couldn't upload (e.g. uploader
		// unavailable or upload failed) so we don't ship invalid URLs upstream
		// that crash the vision pipeline with "Internal error".
		allImages = filterUploadableImageURLs(allImages)
		if len(allImages) > 0 {
			contentParts = append(contentParts, qwen.ContentPart{Type: "text", Text: collapsed})
			for _, url := range allImages {
				contentParts = append(contentParts, qwen.ContentPart{Type: "image", Image: url})
			}
		}
	}

	msg := qwen.Message{
		Role:     "user",
		ChatType: chatType,
		Extra:    extra,
		FeatureConfig: &qwen.FeatureConfig{
			OutputSchema:    "phase",
			ThinkingEnabled: thinkingEnabled,
		},
	}
	if len(contentParts) > 0 {
		msg.ContentParts = contentParts
	} else {
		msg.Content = collapsed
	}
	msgs := []qwen.Message{msg}

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

// filterUploadableImageURLs drops `data:` URIs that should have been uploaded
// upstream already. By the time buildQwenRequestFull runs, the chat handler is
// expected to have replaced every data URI with a Qwen-OSS HTTPS URL via
// uploadDataURIsInPlace. Anything still starting with `data:` here is a sign
// that upload failed; we drop it rather than forwarding an inline data URI
// which qwen.ai's vision model can't fetch.
func filterUploadableImageURLs(urls []string) []string {
	if len(urls) == 0 {
		return nil
	}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if strings.HasPrefix(u, "data:") {
			continue
		}
		out = append(out, u)
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

// lookupConvKey returns the cache key under which a prior turn would have
// stored its chat_id, given THIS request's full message slice.
//
// Storage on turn N: hash(msgs at end of turn N) -> chat_id.
// On turn N+1 the client appends [assistant_N, user_{N+1}]. To recover the
// prior key we drop those two trailing messages. If the trailing shape isn't
// [..., assistant, user] we return "" (no continuation signal).
func lookupConvKey(model string, msgs []openai.ChatMessage) string {
	if len(msgs) < 2 {
		return ""
	}
	last := strings.ToLower(msgs[len(msgs)-1].Role)
	prev := strings.ToLower(msgs[len(msgs)-2].Role)
	if last != "user" || prev != "assistant" {
		return ""
	}
	prefix := msgs[:len(msgs)-2]
	if len(prefix) == 0 {
		return ""
	}
	return promptcache.Key(model, collapseMessages(prefix))
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

// storeSessionAndMaybeSummarize persists session messages and triggers
// rolling summary generation when the turn count threshold is reached.
func (h *handlers) storeSessionAndMaybeSummarize(hash string, reqMsgs []openai.ChatMessage, assistantContent string, token string) {
	if h.sessionStore == nil || !h.deps.Config.Features.SessionPersistence {
		return
	}
	// Build session messages from the request
	msgs := session.MessagesFromOpenAI(reqMsgs)
	if assistantContent != "" {
		msgs = append(msgs, session.Message{Role: "assistant", Content: assistantContent})
	}
	h.sessionStore.AppendMessages(hash, msgs)

	// Check if we should generate a rolling summary
	if !h.deps.Config.Features.RollingSummary {
		return
	}
	everyN := h.deps.Config.Session.SummaryEveryNTurns
	if everyN <= 0 {
		everyN = 5
	}
	turnCount := h.sessionStore.GetTurnCount(hash)
	if turnCount > 0 && turnCount%everyN == 0 {
		summaryModel := h.deps.Config.Session.SummaryModel
		if summaryModel == "" {
			summaryModel = "qwen3-plus"
		}
		currentSummary := h.sessionStore.GetSummary(hash)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					h.deps.Logger.Error("rolling summary panic recovered", "panic", r)
				}
			}()
			ctx := context.Background()
			newSummary, err := session.GenerateSummary(ctx, h.deps.Qwen, token, summaryModel, msgs, currentSummary)
			if err != nil {
				h.deps.Logger.Warn("rolling summary failed", "hash", hash, "err", err)
				return
			}
			if newSummary != "" {
				h.sessionStore.SetSummary(hash, newSummary)
				h.deps.Logger.Info("rolling summary updated", "hash", hash, "len", len(newSummary))
			}
		}()
	}
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

func (h *handlers) collectChatCompletion(body io.ReadCloser, id string, created int64, model string, hasTools bool, inputTokensFallback int) (openai.ChatCompletion, string, bool, error) {
	defer func() {
		_ = body.Close()
	}()
	reader := qwen.NewStreamReader(body)
	var content strings.Builder
	var reasoningContent strings.Builder
	inThinking := false
	finishReason := "stop"
	var upstreamInputTokens, upstreamOutputTokens int
	var hasUpstreamUsage bool

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
		// Capture upstream usage if present
		if evt.Delta != nil && evt.Delta.Usage != nil {
			if evt.Delta.Usage.InputTokens > 0 {
				upstreamInputTokens = evt.Delta.Usage.InputTokens
			}
			if evt.Delta.Usage.OutputTokens > 0 {
				upstreamOutputTokens = evt.Delta.Usage.OutputTokens
			}
			hasUpstreamUsage = true
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			continue
		}
		choice := evt.Delta.Choices[0]
		phase := choice.Delta.Phase
		rawText := choice.Delta.Content
		if phase == "think" {
			reasoningContent.WriteString(rawText)
			inThinking = true
		} else {
			if inThinking {
				inThinking = false
			}
			text, next := wrapThinking(rawText, phase, inThinking)
			inThinking = next
			content.WriteString(text)
		}
		if choice.FinishReason != nil {
			finishReason = *choice.FinishReason
		}
	}
	if inThinking {
		content.WriteString("</think>")
	}

	fullContent := content.String()
	fullReasoning := reasoningContent.String()
	outputTokens := approxTokens(fullContent) + approxTokens(fullReasoning)
	reasoningTokens := approxTokens(fullReasoning)

	promptTokens := inputTokensFallback
	estimated := true
	if hasUpstreamUsage {
		promptTokens = upstreamInputTokens
		outputTokens = upstreamOutputTokens
		estimated = false
	}
	usage := openai.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: outputTokens,
		TotalTokens:      promptTokens + outputTokens,
		Estimated:        estimated,
	}
	if reasoningTokens > 0 {
		usage.CompletionTokensDetails = &openai.CompletionTokensDetails{
			ReasoningTokens: reasoningTokens,
		}
	}
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
						Role:             "assistant",
						Content:          contentPtr,
						ReasoningContent: fullReasoning,
						ToolCalls: result.ToolCalls,
					},
					FinishReason: "tool_calls",
				}},
				Usage: usage,
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
			Message:      openai.ChatMessageOut{Role: "assistant", Content: strPtr(fullContent), ReasoningContent: fullReasoning},
			FinishReason: finishReason,
		}},
		Usage: usage,
	}
	return resp, fullContent, truncated, nil
}

// proxyStream re-emits upstream events as OpenAI-style SSE chunks.
func (h *handlers) proxyStream(ctx context.Context, w http.ResponseWriter, body io.Reader, id string, created int64, model string, hasTools bool, inputTokensFallback int, cont toolcall.Continuer) {
	if hasTools {
		h.proxyStreamWithToolDetection(ctx, w, body, id, created, model, inputTokensFallback, cont)
		return
	}
	h.proxyStreamDirect(ctx, w, body, id, created, model, inputTokensFallback)
}

// proxyStreamDirect is the original streaming path with no tool detection.
func (h *handlers) proxyStreamDirect(ctx context.Context, w http.ResponseWriter, body io.Reader, id string, created int64, model string, inputTokensFallback int) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	reader := qwen.NewStreamReader(body)
	roleSent := false
	inThinking := false
	var outputLen int      // track total output bytes for token estimation
	var reasoningLen int   // track reasoning bytes separately for reasoning_tokens
	var upstreamInputTokens, upstreamOutputTokens int
	var hasUpstreamUsage bool

	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	emit := func(chunk openai.StreamChunk) {
		raw, err := marshalStreamChunk(chunk)
		if err != nil {
			return
		}
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(raw)
		_, _ = w.Write([]byte("\n\n"))
		flush()
	}

	// Track whether the upstream finished cleanly (EOF / [DONE]) or was
	// cut mid-stream. A mid-stream cut becomes finish_reason="length" so
	// clients can detect truncation and decide whether to retry.
	upstreamFinish := "stop"

	keepAlive := EffectiveKeepAlive(h.deps.Config.StreamKeepAliveSeconds)
	pacer := newStreamPacer(reader, keepAlive)
	loopErr := pacer.loop(ctx,
		func(evt qwen.StreamEvent) error {
			if evt.Done {
				return nil
			}
			if evt.Comment != "" {
				// Forward upstream keepalive comments so the client's
				// idle timer keeps resetting.
				writeSSEKeepAlive(w, flusher)
				return nil
			}
			// Capture upstream usage if present (may come in a separate event)
			if evt.Delta != nil && evt.Delta.Usage != nil {
				if evt.Delta.Usage.InputTokens > 0 {
					upstreamInputTokens = evt.Delta.Usage.InputTokens
				}
				if evt.Delta.Usage.OutputTokens > 0 {
					upstreamOutputTokens = evt.Delta.Usage.OutputTokens
				}
				hasUpstreamUsage = true
			}
			if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
				return nil
			}
			choice := evt.Delta.Choices[0]
			role := ""
			if !roleSent {
				role = "assistant"
				roleSent = true
			}
			text := choice.Delta.Content
			phase := choice.Delta.Phase

			// Emit reasoning_content for thinking phase, content for answer phase
			if phase == "think" {
				if text == "" && role == "" && choice.FinishReason == nil {
					return nil
				}
				reasoningLen += len(text)
				chunk := openai.StreamChunk{
					ID:      id,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []openai.StreamChoice{{
						Index:        0,
						Delta:        openai.Delta{Role: role, ReasoningContent: text},
						FinishReason: choice.FinishReason,
					}},
				}
				emit(chunk)
			} else {
				text, inThinking = wrapThinking(text, phase, inThinking)
				if choice.FinishReason != nil {
					upstreamFinish = *choice.FinishReason
				}
				if text == "" && role == "" && choice.FinishReason == nil {
					return nil
				}
				outputLen += len(text)
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
			return nil
		},
		func() { writeSSEKeepAlive(w, flusher) },
	)
	if loopErr != nil && !errors.Is(loopErr, context.Canceled) {
		h.deps.Logger.Warn("stream read error", "err", loopErr)
		upstreamFinish = "length"
	}
	if errors.Is(loopErr, context.Canceled) {
		// Client disconnected — bail without writing more.
		return
	}

	if inThinking {
		outputLen += len("</think>")
		emit(openai.StreamChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []openai.StreamChoice{{Delta: openai.Delta{Content: "</think>"}}},
		})
	}

	// Emit usage in final chunk per OpenAI streaming convention.
	outputTokens := outputLen / 4
	if outputTokens == 0 && outputLen > 0 {
		outputTokens = 1
	}
	reasoningTokens := reasoningLen / 4
	if reasoningTokens == 0 && reasoningLen > 0 {
		reasoningTokens = 1
	}
	// completion_tokens must include reasoning tokens — they are part of the output.
	totalCompletion := outputTokens + reasoningTokens

	promptTokens := inputTokensFallback
	estimated := true
	if hasUpstreamUsage {
		// Prefer real upstream token counts over our estimates.
		promptTokens = upstreamInputTokens
		totalCompletion = upstreamOutputTokens
		estimated = false
	}

	usage := &openai.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: totalCompletion,
		TotalTokens:      promptTokens + totalCompletion,
		Estimated:        estimated,
	}
	if reasoningTokens > 0 {
		usage.CompletionTokensDetails = &openai.CompletionTokensDetails{
			ReasoningTokens: reasoningTokens,
		}
	}
	emit(openai.StreamChunk{
		ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []openai.StreamChoice{{Index: 0, Delta: openai.Delta{}, FinishReason: &upstreamFinish}},
		Usage:  usage,
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	flush()
}

// proxyStreamWithToolDetection buffers the stream to detect <tool_call> blocks.
// Content before the first <tool_call> is streamed normally. Once detected,
// the remainder is buffered and tool calls are emitted as structured deltas.
func (h *handlers) proxyStreamWithToolDetection(ctx context.Context, w http.ResponseWriter, body io.Reader, id string, created int64, model string, inputTokensFallback int, cont toolcall.Continuer) {
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
	var reasoningLen int
	var upstreamInputTokens, upstreamOutputTokens int
	var hasUpstreamUsage bool
	triggered := false
	roleSent := false

	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	emit := func(chunk openai.StreamChunk) {
		raw, err := marshalStreamChunk(chunk)
		if err != nil {
			return
		}
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(raw)
		_, _ = w.Write([]byte("\n\n"))
		flush()
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
			// Capture upstream usage if present
			if evt.Delta != nil && evt.Delta.Usage != nil {
				if evt.Delta.Usage.InputTokens > 0 {
					upstreamInputTokens = evt.Delta.Usage.InputTokens
				}
				if evt.Delta.Usage.OutputTokens > 0 {
					upstreamOutputTokens = evt.Delta.Usage.OutputTokens
				}
				hasUpstreamUsage = true
			}
			if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
				return nil
			}
			choice := evt.Delta.Choices[0]
			phase := choice.Delta.Phase
			rawContent := choice.Delta.Content

			// Emit reasoning_content for thinking phase directly
			if phase == "think" {
				if rawContent != "" {
					reasoningLen += len(rawContent)
					role := ""
					if !roleSent {
						role = "assistant"
						roleSent = true
					}
					emit(openai.StreamChunk{
						ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
						Choices: []openai.StreamChoice{{
							Index: 0,
							Delta: openai.Delta{Role: role, ReasoningContent: rawContent},
						}},
					})
				}
				return nil
			}

			text, next := wrapThinking(rawContent, phase, inThinking)
			inThinking = next
			fullContent.WriteString(text)

			if triggered {
				return nil
			}
			accumulated := fullContent.String()
			if strings.Contains(accumulated, "<tool_call") {
				triggered = true
				return nil
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
			return nil
		},
		func() { writeSSEKeepAlive(w, flusher) },
	)
	truncated := false
	if loopErr != nil && !errors.Is(loopErr, context.Canceled) {
		h.deps.Logger.Warn("stream read error", "err", loopErr)
		truncated = true
	}
	if errors.Is(loopErr, context.Canceled) {
		return
	}

	if inThinking {
		fullContent.WriteString("</think>")
	}

	accumulated := fullContent.String()
	result := toolcall.ParseWithFormats(accumulated, h.deps.Config.Features.MultiFormatToolParsing)

	// Nếu có dấu hiệu bị cắt mà chưa ra tool call → thử xin model viết tiếp.
	if cont != nil &&
		(toolcall.HasUnclosedToolCall(accumulated) ||
			(len(result.ToolCalls) == 0 && toolcall.SawToolMarker(accumulated))) {
		if res, ok := toolcall.ResolveTruncatedToolCall(
			ctx, accumulated, h.deps.Config.Features.MultiFormatToolParsing, cont,
		); ok {
			result = res // rơi xuống nhánh emit tool_calls bên dưới
		}
	}

	if len(result.ToolCalls) > 0 {
		// Emit any remaining content before tool calls that wasn't streamed yet.
		// Use raw accumulated text (not result.Content from parser) to avoid
		// losing content that the parser may have stripped or restructured.
		// Find where <tool_call starts in the raw text to know the boundary.
		toolCallStart := strings.Index(accumulated, "<tool_call")
		if toolCallStart < 0 {
			toolCallStart = len(accumulated)
		}
		if toolCallStart > emittedLen {
			unsent := accumulated[emittedLen:toolCallStart]
			unsent = strings.TrimRight(unsent, " \t\n\r")
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

		// Emit tool calls as incremental streaming deltas.
		// OpenAI spec requires arguments to be streamed in small chunks,
		// not as a single monolithic payload. Claude Code and other clients
		// expect incremental argument deltas with consistent index/id.
		const toolCallArgChunkSize = 80
		for i, tc := range result.ToolCalls {
			role := ""
			if !roleSent {
				role = "assistant"
				roleSent = true
			}
			// First chunk: emit name + id + type + first argument slice
			args := tc.Function.Arguments
			firstChunk := args
			if len(firstChunk) > toolCallArgChunkSize {
				firstChunk = firstChunk[:toolCallArgChunkSize]
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
								Arguments: firstChunk,
							},
						}},
					},
				}},
			})
			// Subsequent chunks: emit remaining argument slices
			for offset := toolCallArgChunkSize; offset < len(args); offset += toolCallArgChunkSize {
				end := offset + toolCallArgChunkSize
				if end > len(args) {
					end = len(args)
				}
				emit(openai.StreamChunk{
					ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
					Choices: []openai.StreamChoice{{
						Index: 0,
						Delta: openai.Delta{
							ToolCalls: []openai.ToolCallDelta{{
								Index: i,
								Function: openai.ToolCallFuncDelta{
									Arguments: args[offset:end],
								},
							}},
						},
					}},
				})
			}
		}

		// Emit usage with tool_calls finish chunk
		outputTokens := approxTokens(accumulated)
		toolCalls := "tool_calls"
		emit(openai.StreamChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []openai.StreamChoice{{Index: 0, Delta: openai.Delta{}, FinishReason: &toolCalls}},
			Usage: &openai.Usage{
				PromptTokens:     inputTokensFallback,
				CompletionTokens: outputTokens,
				TotalTokens:      inputTokensFallback + outputTokens,
			},
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

		// Determine finish reason based on what we observed
		finish := "stop"
		switch {
		case toolcall.HasUnclosedToolCall(accumulated):
			// Model started a <tool_call> but stream cut off before closing it.
			// This is the #1 cause of agents "stopping" after calling a tool.
			finish = "length"
		case toolcall.SawToolMarker(accumulated):
			// Model showed intent to call a tool but parsing yielded 0 calls
			// (malformed JSON that jsonrepair couldn't fix).
			finish = "length"
		case truncated:
			// Upstream stream errored mid-response; signal truncation.
			finish = "length"
		}
		outputTokens := approxTokens(accumulated)
		reasoningTokens := reasoningLen / 4
		if reasoningTokens == 0 && reasoningLen > 0 {
			reasoningTokens = 1
		}
		totalCompletion := outputTokens + reasoningTokens
		promptTokens := inputTokensFallback
		estimated := true
		if hasUpstreamUsage {
			promptTokens = upstreamInputTokens
			totalCompletion = upstreamOutputTokens
			estimated = false
		}
		usage := &openai.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: totalCompletion,
			TotalTokens:      promptTokens + totalCompletion,
			Estimated:        estimated,
		}
		if reasoningTokens > 0 {
			usage.CompletionTokensDetails = &openai.CompletionTokensDetails{
				ReasoningTokens: reasoningTokens,
			}
		}
		emit(openai.StreamChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []openai.StreamChoice{{Index: 0, Delta: openai.Delta{}, FinishReason: &finish}},
			Usage:  usage,
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
