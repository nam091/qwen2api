package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

	var req openai.ResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		h.proxyResponsesStream(w, body, responseID, createdAt, req.Model, hasTools)
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
			if role == "" {
				role = "user"
			}
			msgs = append(msgs, openai.ChatMessage{
				Role:    role,
				Content: item.Content,
			})
		case "function_call_output":
			msgs = append(msgs, openai.ChatMessage{
				Role:       "tool",
				Content:    jsonStringRaw(item.Output),
				ToolCallID: item.CallID,
			})
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
func (h *handlers) proxyResponsesStream(w http.ResponseWriter, body io.Reader, id string, created int64, model string, hasTools bool) {
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

	emitEvent := func(eventType string, data any) {
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

	// Emit response.output_item.added for the message
	msgID := "msg_" + uuid.NewString()
	emitEvent("response.output_item.added", openai.ResponseStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item: &openai.ResponseOutputItem{
			Type:   "message",
			ID:     msgID,
			Status: "in_progress",
			Role:   "assistant",
		},
	})

	// Emit response.content_part.added
	emitEvent("response.content_part.added", openai.ResponseStreamEvent{
		Type:         "response.content_part.added",
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

		emitEvent("response.output_text.delta", openai.ResponseStreamEvent{
			Type:         "response.output_text.delta",
			OutputIndex:  0,
			ContentIndex: contentIdx,
			Delta:        text,
		})
	}

	if inThinking {
		fullContent.WriteString("</think>")
		emitEvent("response.output_text.delta", openai.ResponseStreamEvent{
			Type:         "response.output_text.delta",
			OutputIndex:  0,
			ContentIndex: contentIdx,
			Delta:        "</think>",
		})
	}

	accumulated := fullContent.String()

	// Check for tool calls
	if hasTools {
		result := toolcall.ParseWithFormats(accumulated, h.deps.Config.Features.MultiFormatToolParsing)
		if len(result.ToolCalls) > 0 {
			// Emit function_call items
			for i, tc := range result.ToolCalls {
				emitEvent("response.output_item.added", openai.ResponseStreamEvent{
					Type:        "response.output_item.added",
					OutputIndex: i + 1,
					Item: &openai.ResponseOutputItem{
						Type:      "function_call",
						ID:        "fc_" + uuid.NewString(),
						Status:    "completed",
						CallID:    tc.ID,
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
	} else {
		// Emit done events for the text content (only when no tool calls)
		emitEvent("response.output_text.done", openai.ResponseStreamEvent{
			Type:         "response.output_text.done",
			OutputIndex:  0,
			ContentIndex: 0,
			ItemID:       msgID,
			Text:         accumulated,
		})
		emitEvent("response.content_part.done", openai.ResponseStreamEvent{
			Type:         "response.content_part.done",
			OutputIndex:  0,
			ContentIndex: 0,
			ItemID:       msgID,
			Part: &openai.ResponseContentBlock{
				Type: "output_text",
				Text: accumulated,
			},
		})
		emitEvent("response.output_item.done", openai.ResponseStreamEvent{
			Type:        "response.output_item.done",
			OutputIndex: 0,
			Item: &openai.ResponseOutputItem{
				Type:   "message",
				ID:     msgID,
				Status: "completed",
				Role:   "assistant",
				Content: []openai.ResponseContentBlock{{
					Type: "output_text",
					Text: accumulated,
				}},
			},
		})
	}

	// Build the final output from the content we already accumulated.
	var finalOutput []openai.ResponseOutputItem
	if hasTools {
		result := toolcall.ParseWithFormats(accumulated, h.deps.Config.Features.MultiFormatToolParsing)
		if len(result.ToolCalls) > 0 {
			if strings.TrimSpace(result.Content) != "" {
				finalOutput = append(finalOutput, openai.ResponseOutputItem{
					Type:   "message",
					ID:     msgID,
					Status: "completed",
					Role:   "assistant",
					Content: []openai.ResponseContentBlock{{
						Type: "output_text",
						Text: result.Content,
					}},
				})
			}
			for _, tc := range result.ToolCalls {
				finalOutput = append(finalOutput, openai.ResponseOutputItem{
					Type:      "function_call",
					ID:        "fc_" + uuid.NewString(),
					Status:    "completed",
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		} else {
			finalOutput = append(finalOutput, openai.ResponseOutputItem{
				Type:   "message",
				ID:     msgID,
				Status: "completed",
				Role:   "assistant",
				Content: []openai.ResponseContentBlock{{
					Type: "output_text",
					Text: accumulated,
				}},
			})
		}
	} else {
		finalOutput = append(finalOutput, openai.ResponseOutputItem{
			Type:   "message",
			ID:     msgID,
			Status: "completed",
			Role:   "assistant",
			Content: []openai.ResponseContentBlock{{
				Type: "output_text",
				Text: accumulated,
			}},
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
