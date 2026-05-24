package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keaume34/qwen2api/internal/config"
	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/qwen"
	"github.com/keaume34/qwen2api/internal/tokenpool"
)

func newTestServer(t *testing.T, upstream *httptest.Server) http.Handler {
	t.Helper()
	cfg := config.Config{
		Port:            5001,
		APIKeys:         []config.APIKey{{Value: "sk-test"}},
		Tokens:          []config.Token{{Value: "tok-1"}},
		BaseURL:         upstream.URL,
		CooldownSeconds: 60,
		LogLevel:        "error",
		Retry:           config.RetryConfig{MaxAttempts: 3},
		Features: config.FeatureToggles{
			APIKeyRotation: true,
		},
	}
	client := qwen.NewClient(qwen.ClientConfig{
		BaseURL:        upstream.URL,
		TimeoutSeconds: 10,
	})
	pool := tokenpool.New(cfg.Tokens, time.Minute)
	return New(Deps{
		Config:    &cfg,
		Logger:    slog.Default(),
		Qwen:      client,
		TokenPool: pool,
	})
}

func TestUnauthorizedWithoutKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	h := newTestServer(t, upstream)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d want 401", rec.Code)
	}
}

func TestHealth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	h := newTestServer(t, upstream)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d want 200", rec.Code)
	}
}

// fakeUpstream mounts the minimum chat.qwen.ai endpoints used by qwen2api.
func fakeUpstream(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/chats/new", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"chat-abc"}}`))
	})
	mux.HandleFunc("/api/v2/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		writeData := func(s string) {
			fmt.Fprintf(w, "data: %s\n\n", s)
			if fl != nil {
				fl.Flush()
			}
		}
		writeData(`{"choices":[{"delta":{"role":"assistant","content":"Hel","phase":"answer"}}]}`)
		writeData(`{"choices":[{"delta":{"content":"lo","phase":"answer"}}]}`)
		writeData(`[DONE]`)
	})
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen3-max"},{"id":"qwen-plus"}]}`))
	})
	return httptest.NewServer(mux)
}

func TestChatCompletionsNonStream(t *testing.T) {
	upstream := fakeUpstream(t)
	defer upstream.Close()
	h := newTestServer(t, upstream)

	body := `{"model":"qwen3-max","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	choices, _ := resp["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v", choices)
	}
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "Hello" {
		t.Errorf("content = %q want Hello", msg["content"])
	}
}

func TestChatCompletionsStream(t *testing.T) {
	upstream := fakeUpstream(t)
	defer upstream.Close()
	h := newTestServer(t, upstream)

	body := `{"model":"qwen3-max","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}
	out, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(out), "data: [DONE]") {
		t.Errorf("stream missing [DONE]; got: %s", out)
	}
	if !strings.Contains(string(out), `"role":"assistant"`) {
		t.Errorf("stream missing role; got: %s", out)
	}
	if !strings.Contains(string(out), `Hel`) || !strings.Contains(string(out), `lo`) {
		t.Errorf("stream missing content chunks; got: %s", out)
	}
}

func TestModelsListUpstream(t *testing.T) {
	upstream := fakeUpstream(t)
	defer upstream.Close()
	h := newTestServer(t, upstream)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "qwen3-max") {
		t.Errorf("expected qwen3-max in body; got %s", rec.Body.String())
	}
}

func TestWrapThinking(t *testing.T) {
	cases := []struct {
		content  string
		phase    string
		thinkIn  bool
		want     string
		thinkOut bool
	}{
		{"abc", "think", false, "<think>abc", true},
		{"def", "think", true, "def", true},
		{"ghi", "answer", true, "</think>ghi", false},
		{"jkl", "answer", false, "jkl", false},
		{"", "", false, "", false},
	}
	for i, c := range cases {
		got, gotIn := wrapThinking(c.content, c.phase, c.thinkIn)
		if got != c.want || gotIn != c.thinkOut {
			t.Errorf("case %d: got (%q,%v) want (%q,%v)", i, got, gotIn, c.want, c.thinkOut)
		}
	}
}

func msg(role, content string) openai.ChatMessage {
	return openai.ChatMessage{Role: role, Content: json.RawMessage(`"` + content + `"`)}
}

func TestBuildQwenRequestSingleUserPassthrough(t *testing.T) {
	req := openai.ChatRequest{
		Model:    "qwen3-max",
		Messages: []openai.ChatMessage{msg("user", "hi")},
	}
	out := buildQwenRequest(req)
	if len(out.Messages) != 1 {
		t.Fatalf("messages = %d want 1", len(out.Messages))
	}
	if out.Messages[0].Role != "user" {
		t.Errorf("role = %q want user", out.Messages[0].Role)
	}
	if out.Messages[0].Content != "hi" {
		t.Errorf("content = %q want %q (single-user should passthrough untouched)", out.Messages[0].Content, "hi")
	}
}

func TestBuildQwenRequestCollapsesHistory(t *testing.T) {
	cases := []struct {
		name string
		in   []openai.ChatMessage
		want string
	}{
		{
			name: "two user messages",
			in: []openai.ChatMessage{
				msg("user", "My favorite drink is coffee."),
				msg("user", "What is my favorite drink?"),
			},
			want: "User: My favorite drink is coffee.\n\nUser: What is my favorite drink?\n\nAssistant:",
		},
		{
			name: "system+user",
			in: []openai.ChatMessage{
				msg("system", "You only say YES."),
				msg("user", "Is the sky blue?"),
			},
			want: "System: You only say YES.\n\nUser: Is the sky blue?\n\nAssistant:",
		},
		{
			name: "user+assistant+user",
			in: []openai.ChatMessage{
				msg("user", "What is 2+2?"),
				msg("assistant", "4"),
				msg("user", "What was my first question?"),
			},
			want: "User: What is 2+2?\n\nAssistant: 4\n\nUser: What was my first question?\n\nAssistant:",
		},
		{
			name: "trailing assistant turn no nudge",
			in: []openai.ChatMessage{
				msg("user", "hi"),
				msg("assistant", "hello"),
			},
			want: "User: hi\n\nAssistant: hello",
		},
		{
			name: "skips empty turns cleanly",
			in: []openai.ChatMessage{
				msg("system", ""),
				msg("user", "hi"),
			},
			want: "User: hi\n\nAssistant:",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := buildQwenRequest(openai.ChatRequest{Model: "qwen3-max", Messages: c.in})
			if len(out.Messages) != 1 {
				t.Fatalf("messages = %d want 1 (must collapse)", len(out.Messages))
			}
			if out.Messages[0].Role != "user" {
				t.Errorf("role = %q want user", out.Messages[0].Role)
			}
			if out.Messages[0].Content != c.want {
				t.Errorf("content = %q\nwant      %q", out.Messages[0].Content, c.want)
			}
		})
	}
}

// fakeUpstreamWithToolCall returns a fake upstream that emits a tool call in text.
func fakeUpstreamWithToolCall(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/chats/new", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"chat-tool"}}`))
	})
	mux.HandleFunc("/api/v2/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		writeData := func(s string) {
			fmt.Fprintf(w, "data: %s\n\n", s)
			if fl != nil {
				fl.Flush()
			}
		}
		writeData(`{"choices":[{"delta":{"role":"assistant","content":"Let me read that file.\n<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"/foo.go\"}}\n</tool_call>","phase":"answer"}}]}`)
		writeData(`[DONE]`)
	})
	return httptest.NewServer(mux)
}

func TestChatCompletionsNonStreamWithToolCall(t *testing.T) {
	upstream := fakeUpstreamWithToolCall(t)
	defer upstream.Close()
	h := newTestServer(t, upstream)

	body := `{"model":"qwen3-max","stream":false,"messages":[{"role":"user","content":"read /foo.go"}],"tools":[{"type":"function","function":{"name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp openai.ChatCompletion
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d want 1", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q want tool_calls", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d want 1", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.Function.Name != "read_file" {
		t.Errorf("tool_call name = %q want read_file", tc.Function.Name)
	}
	if tc.Type != "function" {
		t.Errorf("tool_call type = %q want function", tc.Type)
	}
	if !strings.HasPrefix(tc.ID, "call_") {
		t.Errorf("tool_call id = %q want prefix call_", tc.ID)
	}
}

func TestChatCompletionsStreamWithToolCall(t *testing.T) {
	upstream := fakeUpstreamWithToolCall(t)
	defer upstream.Close()
	h := newTestServer(t, upstream)

	body := `{"model":"qwen3-max","stream":true,"messages":[{"role":"user","content":"read /foo.go"}],"tools":[{"type":"function","function":{"name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}

	out := rec.Body.String()
	if !strings.Contains(out, "data: [DONE]") {
		t.Errorf("stream missing [DONE]; got: %s", out)
	}
	if !strings.Contains(out, `"tool_calls"`) {
		t.Errorf("stream missing tool_calls; got: %s", out)
	}
	if !strings.Contains(out, `"read_file"`) {
		t.Errorf("stream missing function name; got: %s", out)
	}
	if !strings.Contains(out, `"tool_calls"`) && !strings.Contains(out, `"finish_reason":"tool_calls"`) {
		t.Errorf("stream missing finish_reason tool_calls; got: %s", out)
	}
}

func TestChatCompletionsNoToolsNoRegression(t *testing.T) {
	upstream := fakeUpstream(t)
	defer upstream.Close()
	h := newTestServer(t, upstream)

	body := `{"model":"qwen3-max","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp openai.ChatCompletion
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q want stop", resp.Choices[0].FinishReason)
	}
	if resp.Choices[0].Message.ToolCalls != nil {
		t.Errorf("tool_calls should be nil when no tools in request")
	}
	if *resp.Choices[0].Message.Content != "Hello" {
		t.Errorf("content = %q want Hello", *resp.Choices[0].Message.Content)
	}
}

func fakeUpstreamTruncatedThenContinues(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	var completionCount int
	mux.HandleFunc("/api/v2/chats/new", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"chat-tool"}}`))
	})
	mux.HandleFunc("/api/v2/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		completionCount++
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		writeData := func(s string) {
			fmt.Fprintf(w, "data: %s\n\n", s)
			if fl != nil {
				fl.Flush()
			}
		}
		if completionCount == 1 {
			writeData(`{"choices":[{"delta":{"role":"assistant","content":"<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"/foo.go\"}","phase":"answer"}}]}`)
			writeData(`[DONE]`)
			return
		}
		writeData(`{"choices":[{"delta":{"role":"assistant","content":"<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"/foo.go\"}}\n</tool_call>","phase":"answer"}}]}`)
		writeData(`[DONE]`)
	})
	return httptest.NewServer(mux)
}

func TestChatCompletionsNonStreamTruncatedToolCallAutoContinue(t *testing.T) {
	upstream := fakeUpstreamTruncatedThenContinues(t)
	defer upstream.Close()
	h := newTestServer(t, upstream)

	body := `{"model":"qwen3-max","stream":false,"messages":[{"role":"user","content":"read /foo.go"}],"tools":[{"type":"function","function":{"name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp openai.ChatCompletion
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q want tool_calls", resp.Choices[0].FinishReason)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d want 1", len(resp.Choices[0].Message.ToolCalls))
	}
	if resp.Choices[0].Message.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("tool_call name = %q want read_file", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	}
}

func TestIsTruncatedToolCallContent(t *testing.T) {
	if !isTruncatedToolCallContent("<tool_call>\n{\"name\":\"x\"\n") {
		t.Fatal("expected truncated content to be detected")
	}
	if isTruncatedToolCallContent("<tool_call>\n{\"name\":\"x\"}\n</tool_call>") {
		t.Fatal("did not expect complete tool_call to be detected as truncated")
	}
}

func TestCollapseMessagesWithToolRole(t *testing.T) {
	msgs := []openai.ChatMessage{
		msg("user", "read the file"),
		{
			Role:       "assistant",
			Content:    json.RawMessage(`""`),
			ToolCalls:  []openai.ToolCall{{ID: "call_abc", Type: "function", Function: openai.ToolCallFunction{Name: "read_file", Arguments: `{"path":"/x.go"}`}}},
		},
		{
			Role:       "tool",
			Content:    json.RawMessage(`"file contents here"`),
			ToolCallID: "call_abc",
		},
		msg("user", "summarize it"),
	}
	out := collapseMessages(msgs)
	if !strings.Contains(out, "Tool [call_id=call_abc]") {
		t.Errorf("expected tool call_id label in output; got: %s", out)
	}
	if !strings.Contains(out, "<tool_call>") {
		t.Errorf("expected <tool_call> block for assistant tool_calls; got: %s", out)
	}
	if !strings.Contains(out, "read_file") {
		t.Errorf("expected function name in output; got: %s", out)
	}
}

// fakeUpstreamToolCall returns SSE chunks that include a <tool_call>...</tool_call>
// block emitted mid-stream (mimicking how Qwen models actually emit tool calls
// when prompted by codex CLI with `tools` in the request).
func fakeUpstreamToolCall(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/chats/new", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"chat-tc"}}`))
	})
	mux.HandleFunc("/api/v2/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		writeData := func(s string) {
			fmt.Fprintf(w, "data: %s\n\n", s)
			if fl != nil {
				fl.Flush()
			}
		}
		// Some prose, then a tool_call block, then more prose.
		writeData(`{"choices":[{"delta":{"role":"assistant","content":"I'll run a command. ","phase":"answer"}}]}`)
		writeData(`{"choices":[{"delta":{"content":"<tool_call>","phase":"answer"}}]}`)
		writeData(`{"choices":[{"delta":{"content":"{\"name\": \"shell_command\", \"arguments\": {\"command\": \"ls\"}}","phase":"answer"}}]}`)
		writeData(`{"choices":[{"delta":{"content":"</tool_call>","phase":"answer"}}]}`)
		writeData(`{"choices":[{"delta":{"content":" Done.","phase":"answer"}}]}`)
		writeData(`[DONE]`)
	})
	return httptest.NewServer(mux)
}

// TestResponsesStreamStripsToolCall ensures the /v1/responses streaming
// handler never forwards <tool_call>...</tool_call> blocks to the client as
// text deltas — those must be emitted as proper function_call output items
// instead. Codex/claude-code otherwise render the JSON block visibly in the
// assistant message, which is the bug we are fixing here.
func TestResponsesStreamStripsToolCall(t *testing.T) {
	upstream := fakeUpstreamToolCall(t)
	defer upstream.Close()
	h := newTestServer(t, upstream)

	body := `{
		"model":"qwen3.7-max",
		"stream":true,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"ls please"}]}],
		"tools":[{"type":"function","name":"shell_command","description":"run","parameters":{"type":"object"}}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, "data: [DONE]") {
		t.Errorf("stream missing [DONE]; got: %s", out)
	}
	// Extract delta payloads. The clean prose around the tool_call should be
	// streamed; the tool_call block itself must not appear in any delta.
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if ev["type"] != "response.output_text.delta" {
			continue
		}
		delta, _ := ev["delta"].(string)
		if strings.Contains(delta, "<tool_call") || strings.Contains(delta, "</tool_call>") {
			t.Errorf("output_text.delta leaked a <tool_call> block: %q", delta)
		}
		if strings.Contains(delta, `"name": "shell_command"`) || strings.Contains(delta, `"arguments"`) {
			t.Errorf("output_text.delta leaked tool_call JSON arguments: %q", delta)
		}
	}
	// The function_call must be emitted as its own output item.
	if !strings.Contains(out, `"type":"function_call"`) {
		t.Errorf("expected a function_call output item in stream; got: %s", out)
	}
	if !strings.Contains(out, `"name":"shell_command"`) {
		t.Errorf("expected tool name in function_call item; got: %s", out)
	}
	// Prose around the block should survive.
	if !strings.Contains(out, "I'll run a command") {
		t.Errorf("expected leading prose to survive; got: %s", out)
	}
	if !strings.Contains(out, "Done.") {
		t.Errorf("expected trailing prose to survive; got: %s", out)
	}
}
