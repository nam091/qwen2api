package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/ossupload"
	"github.com/keaume34/qwen2api/internal/session"
)

// RateLimiter limits concurrent upstream requests to prevent Qwen rate limiting.
type RateLimiter struct {
	ch     chan struct{}
	mu     sync.Mutex
	active int
}

// NewRateLimiter creates a rate limiter with max concurrent requests.
func NewRateLimiter(maxConcurrent int) *RateLimiter {
	return &RateLimiter{
		ch: make(chan struct{}, maxConcurrent),
	}
}

// Acquire waits for a slot to become available. Returns a release function.
func (rl *RateLimiter) Acquire() func() {
	rl.ch <- struct{}{}
	rl.mu.Lock()
	rl.active++
	rl.mu.Unlock()
	return func() {
		<-rl.ch
		rl.mu.Lock()
		rl.active--
		rl.mu.Unlock()
	}
}

// Active returns the number of currently active requests.
func (rl *RateLimiter) Active() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.active
}

type handlers struct {
	deps          Deps
	imageUploader *ossupload.Uploader
	imageCache    *imageUploadCache
	sessionStore  *session.Store
	claudeCodeOpt *ClaudeCodeOptimizer
	rateLimiter   *RateLimiter
}

func (h *handlers) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handlers) ready(w http.ResponseWriter, _ *http.Request) {
	status := http.StatusOK
	body := map[string]any{"status": "ok", "tokens": h.deps.TokenPool.Size()}
	if h.deps.TokenPool.Size() == 0 {
		status = http.StatusServiceUnavailable
		body["status"] = "no-tokens"
	}
	writeJSON(w, status, body)
}

// authMiddleware enforces the configured client API keys when any are set.
func (h *handlers) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.deps.Config.Features.APIKeyRotation || len(h.deps.Config.APIKeys) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		key := bearerOrQuery(r)
		if !h.deps.Config.AuthorizedKey(key) {
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerOrQuery(r *http.Request) string {
	if q := r.URL.Query().Get("api_key"); q != "" {
		return strings.TrimSpace(q)
	}
	if h := r.Header.Get("Authorization"); h != "" {
		if strings.HasPrefix(h, "Bearer ") {
			return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
		return strings.TrimSpace(h)
	}
	if k := r.Header.Get("X-Api-Key"); k != "" {
		return strings.TrimSpace(k)
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openai.ErrorEnvelope{
		Error: openai.ErrorBody{Message: msg, Type: code, Code: code},
	})
}

func unixNow() int64 { return time.Now().Unix() }
