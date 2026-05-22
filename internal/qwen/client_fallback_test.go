package qwen

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keaume34/qwen2api/internal/config"
)

func TestModelsWithoutFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer upstream.Close()

	client := NewClient(ClientConfig{
		BaseURL:                upstream.URL,
		TimeoutSeconds:         10,
		BrowserFallbackEnabled: false,
	})

	_, err := client.Models(context.Background(), "test-token")
	if err == nil {
		t.Fatal("expected error when fallback disabled")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 error, got: %v", err)
	}
}

func TestModelsWithFallbackButNoAntiBot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"qwen3-max"}]}`))
	}))
	defer upstream.Close()

	client := NewClient(ClientConfig{
		BaseURL:                upstream.URL,
		TimeoutSeconds:         10,
		BrowserFallbackEnabled: true,
	})

	resp, err := client.Models(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "qwen3-max" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestNewChatWithoutFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer upstream.Close()

	client := NewClient(ClientConfig{
		BaseURL:                upstream.URL,
		TimeoutSeconds:         10,
		BrowserFallbackEnabled: false,
	})

	_, err := client.NewChat(context.Background(), "test-token", "qwen3-max", "chat")
	if err == nil {
		t.Fatal("expected error when fallback disabled")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected 429 error, got: %v", err)
	}
}

func TestNewChatWithFallbackButNoAntiBot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"id":"chat-123"}}`))
	}))
	defer upstream.Close()

	client := NewClient(ClientConfig{
		BaseURL:                upstream.URL,
		TimeoutSeconds:         10,
		BrowserFallbackEnabled: true,
	})

	chatID, err := client.NewChat(context.Background(), "test-token", "qwen3-max", "chat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chatID != "chat-123" {
		t.Errorf("expected chat-123, got: %s", chatID)
	}
}

func TestCompletionsWithoutFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer upstream.Close()

	client := NewClient(ClientConfig{
		BaseURL:                upstream.URL,
		TimeoutSeconds:         10,
		BrowserFallbackEnabled: false,
	})

	req := CompletionRequest{
		Model:    "qwen3-max",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}
	_, err := client.Completions(context.Background(), "test-token", req)
	if err == nil {
		t.Fatal("expected error when fallback disabled")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 error, got: %v", err)
	}
}

func TestCompletionsWithFallbackButNoAntiBot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	client := NewClient(ClientConfig{
		BaseURL:                upstream.URL,
		TimeoutSeconds:         10,
		BrowserFallbackEnabled: true,
	})

	req := CompletionRequest{
		Model:    "qwen3-max",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}
	body, err := client.Completions(context.Background(), "test-token", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	if !strings.Contains(string(data), "hi") {
		t.Errorf("expected content in stream, got: %s", data)
	}
}

func TestIsAntiBotResponse(t *testing.T) {
	cases := []struct {
		name        string
		statusCode  int
		contentType string
		body        string
		want        bool
	}{
		{"403 forbidden", http.StatusForbidden, "", "", true},
		{"429 rate limit", http.StatusTooManyRequests, "", "", true},
		{"503 with html", 503, "text/html", "", true},
		{"503 with challenge", 503, "", "cloudflare challenge", true},
		{"200 ok", http.StatusOK, "", "", false},
		{"500 error", http.StatusInternalServerError, "", "", false},
		{"captcha in body", http.StatusOK, "", "please solve captcha", true},
		{"verify in body", http.StatusOK, "", "verify you are human", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: c.statusCode,
				Header:     http.Header{},
			}
			if c.contentType != "" {
				resp.Header.Set("Content-Type", c.contentType)
			}
			got := isAntiBotResponse(resp, c.body)
			if got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestFallbackWithDynamicConfig(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"qwen3-max"}]}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Features: config.FeatureToggles{
			BrowserEngineFallback: true,
		},
	}

	client := NewClient(ClientConfig{
		BaseURL:                upstream.URL,
		TimeoutSeconds:         10,
		BrowserFallbackEnabled: false, // static config says false
	})
	client.SetConfigRef(&cfg) // but dynamic config says true

	// Should use dynamic config value (true)
	resp, err := client.Models(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Errorf("unexpected response: %+v", resp)
	}
}
