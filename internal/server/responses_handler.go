package server

import (
	"encoding/json"
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
	"github.com/keaume34/qwen2api/internal/qwen"
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

	maxAttempts := 1
	if h.deps.Config.Features.RetryOnTokenFailure && h.deps.Config.Retry.MaxAttempts > 1 {
		maxAttempts = h.deps.Config.Retry.MaxAttempts
	}

	var (
		token config.Token
		body  io.ReadCloser
		retries int
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

		chatID, err := h.deps.Qwen.NewChat(r.Context(), token.Value, upstreamReq.Model, upstreamReq.ChatType)
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
		upstreamReq.ChatID = chatID

		body, err = h.deps.Qwen.Completions(r.Context(), token.Value, upstreamReq)
		if err != nil {
			if shouldRetry(err) && attempt < maxAttempts {
				retries++
				h.markBadAndLog(token.Value, err, "open completion stream (responses)", attempt)
				continue
			}
			h.handleUpstreamFailure(w, token.Value, err, "open completion stream")
			h.logRequestEndpoint(r, chatReq, "responses", token.Value, http.StatusBadGateway, time.Since(start), false, retries, err)
			return
		}
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

	if req.Stream {
		defer func() { _ = body.Close() }()
		h.proxyResponsesStream(w, body, responseID, createdAt, req.Model, hasTools, h.deps.Config.Features.MultiFormatToolParsing)
		h.metricsObserve("qwen2api_request_duration_seconds", time.Since(start).Seconds())
		h.logRequestEndpoint(r, chatReq, "responses", token.Value, http.StatusOK, time.Since(start), false, retries, nil)
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
	h.logRequestEndpoint(r, chatReq, "responses", token.Value, http.StatusOK, time.Since(start), false, retries, nil)
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
		text, next := wrapThinking(choice.Delta.Content, choice.Delta.Phase, inThinking)
		inThinking = next
		content.WriteString(text)
	}
	if inThinking {
		content.WriteString("</think>")
	}

	fullContent := content.String()
	truncated := hasTools && isTruncatedToolCallContent(fullContent)

	var output []openai.ResponseOutputItem

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
func (h *handlers) proxyResponsesStream(w http.ResponseWriter, body io.Reader, id string, created int64, model string, hasTools, multiFormat bool) {
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
	msgID := "msg_" + uuid.NewString()
	emitRaw := func(eventType string, payload any) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, raw)
		flush()
	}
	emitRaw("response.output_item.added", map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": seq,
		"output_index":    0,
		"item": map[string]any{
			"type":    "message",
			"id":      msgID,
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		},
	})
	seq++

	// Emit response.content_part.added
	emitEvent("response.content_part.added", openai.ResponseStreamEvent{
		Type:         "response.content_part.added",
		ItemID:       msgID,
		OutputIndex:  0,
		ContentIndex: 0,
		Part: &openai.ResponseContentBlock{
			Type: "output_text",
			Text: "",
		},
	})

	reader := qwen.NewStreamReader(body)
	inThinking := false
	var fullContent strings.Builder
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
		emitEvent("response.output_text.delta", openai.ResponseStreamEvent{
			Type:         "response.output_text.delta",
			ItemID:       msgID,
			OutputIndex:  0,
			ContentIndex: contentIdx,
			Delta:        s,
		})
	}

	for {
		evt, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.deps.Logger.Warn("responses stream read error", "err", err)
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
		if text == "" {
			continue
		}
		fullContent.WriteString(text)

		if !hasTools {
			// No tool support: stream every byte as soon as it arrives.
			emitTextDelta(text)
			emittedLen = fullContent.Len()
			continue
		}

		if triggered {
			// Already inside / past a suspected tool call. Buffer everything
			// and decide at the end of the stream.
			continue
		}

		accumulated := fullContent.String()
		if strings.Contains(accumulated, "<tool_call") ||
			(multiFormat && strings.Contains(accumulated, "<function_calls")) {
			triggered = true
			continue
		}

		// Hold back the longest possible tag prefix so we don't leak a
		// partial "<tool_ca" to the client. Align to the start of a rune so
		// we never split UTF-8 sequences mid-codepoint.
		safe := len(accumulated) - holdback
		if safe <= emittedLen {
			continue
		}
		for safe > emittedLen && !utf8.RuneStart(accumulated[safe]) {
			safe--
		}
		if safe > emittedLen {
			emitTextDelta(accumulated[emittedLen:safe])
			emittedLen = safe
		}
	}

	if inThinking {
		fullContent.WriteString("</think>")
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
	emitEvent("response.output_text.done", openai.ResponseStreamEvent{
		Type:         "response.output_text.done",
		OutputIndex:  0,
		ContentIndex: 0,
		ItemID:       msgID,
		Text:         textContent,
	})
	emitEvent("response.content_part.done", openai.ResponseStreamEvent{
		Type:         "response.content_part.done",
		OutputIndex:  0,
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
		"output_index":    0,
		"item": map[string]any{
			"type":    "message",
			"id":      msgID,
			"status":  "completed",
			"role":    "assistant",
			"content": []map[string]any{{"type": "output_text", "text": textContent}},
		},
	})
	seq++

	// Emit function_call items after the message.
	for i, tc := range toolCalls {
		fcID := "fc_" + uuid.NewString()
		emitEvent("response.output_item.added", openai.ResponseStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: i + 1,
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
			OutputIndex: i + 1,
			ItemID:      fcID,
			Delta:       tc.Function.Arguments,
		})
		emitEvent("response.function_call_arguments.done", openai.ResponseStreamEvent{
			Type:        "response.function_call_arguments.done",
			OutputIndex: i + 1,
			ItemID:      fcID,
			Arguments:   tc.Function.Arguments,
		})
		emitEvent("response.output_item.done", openai.ResponseStreamEvent{
			Type:        "response.output_item.done",
			OutputIndex: i + 1,
			Item: &openai.ResponseOutputItem{
				Type:      "function_call",
				ID:        fcID,
				Status:    "completed",
				CallID:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	// Final output array mirrors what was emitted as items.
	finalOutput := []openai.ResponseOutputItem{{
		Type:   "message",
		ID:     msgID,
		Status: "completed",
		Role:   "assistant",
		Content: []openai.ResponseContentBlock{{
			Type: "output_text",
			Text: textContent,
		}},
	}}
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

	emitEvent("response.completed", openai.ResponseStreamEvent{
		Type: "response.completed",
		Response: &openai.ResponseObject{
			ID:        id,
			Object:    "response",
			CreatedAt: created,
			Status:    "completed",
			Model:     model,
			Output:    finalOutput,
		},
	})

	fmt.Fprint(w, "data: [DONE]\n\n")
	flush()
}
