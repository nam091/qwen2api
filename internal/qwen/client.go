package qwen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keaume34/qwen2api/internal/browserengine"
	"github.com/keaume34/qwen2api/internal/config"
)

// ClientConfig configures the upstream HTTP client.
type ClientConfig struct {
	BaseURL        string
	UserAgent      string
	SsxmodItna     string
	Ssxmodi2       string
	TimeoutSeconds int
	// PoolingEnabled enables connection pooling for both regular and stream clients.
	PoolingEnabled bool
	// MaxIdleConns caps the total number of idle keep-alive connections.
	MaxIdleConns int
	// MaxIdleConnsPerHost caps per-host idle connections.
	MaxIdleConnsPerHost int
	// IdleConnTimeoutSeconds is how long an idle connection stays in the pool.
	IdleConnTimeoutSeconds int
	// BrowserFallbackEnabled enables fallback to browser engine on anti-bot blocks.
	BrowserFallbackEnabled bool
}

// Client talks to chat.qwen.ai.
type Client struct {
	cfg       ClientConfig
	http      *http.Client
	stream    *http.Client
	configRef *config.Config
	browser   *browserengine.HybridEngine
}

// NewClient constructs a Client with sensible defaults.
func NewClient(cfg ClientConfig) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://chat.qwen.ai"
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0"
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}

	var transport *http.Transport
	if cfg.PoolingEnabled {
		base := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.MaxIdleConns > 0 {
			base.MaxIdleConns = cfg.MaxIdleConns
		} else {
			base.MaxIdleConns = 200
		}
		if cfg.MaxIdleConnsPerHost > 0 {
			base.MaxIdleConnsPerHost = cfg.MaxIdleConnsPerHost
		} else {
			base.MaxIdleConnsPerHost = 64
		}
		idle := time.Duration(cfg.IdleConnTimeoutSeconds) * time.Second
		if idle <= 0 {
			idle = 90 * time.Second
		}
		base.IdleConnTimeout = idle
		base.ForceAttemptHTTP2 = true
		transport = base
	}

	httpClient := &http.Client{Timeout: timeout}
	if transport != nil {
		httpClient.Transport = transport
	}

	// Stream client gets its OWN transport to avoid HTTP/2 multiplexing
	// failures killing all concurrent streams on the same connection.
	// HTTP/1.1 with keep-alive is more resilient for long-lived SSE streams
	// because each stream has its own TCP connection.
	streamTransport := http.DefaultTransport.(*http.Transport).Clone()
	streamTransport.ForceAttemptHTTP2 = false // Force HTTP/1.1 for streams
	streamTransport.MaxIdleConnsPerHost = 32
	streamTransport.IdleConnTimeout = 5 * time.Minute // Longer than upstream keepalive
	streamTransport.DisableCompression = false
	streamClient := &http.Client{
		Transport: streamTransport,
		// No timeout — streams can run indefinitely. Context cancellation
		// handles cleanup when the downstream client disconnects.
	}

	var browser *browserengine.HybridEngine
	if cfg.BrowserFallbackEnabled {
		browser = browserengine.NewHybridEngine(httpClient, nil, nil, browserengine.HTTPFirst)
	}

	return &Client{
		cfg:     cfg,
		http:    httpClient,
		stream:  streamClient,
		browser: browser,
	}
}

// SetConfigRef sets the config reference for dynamic checks.
func (c *Client) SetConfigRef(cfg *config.Config) {
	c.configRef = cfg
}

// applyHeaders adds the standard set of headers expected by chat.qwen.ai. The
// upstream is strict about `Version`, `source` and a few sec-fetch hints — without
// them /api/v2/chat/completions returns 400 Bad_Request even with a valid token.
func (c *Client) applyHeaders(req *http.Request, token string) {
	origin := strings.TrimRight(c.cfg.BaseURL, "/")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("source", "web")
	req.Header.Set("Version", "0.2.50")
	req.Header.Set("bx-v", "2.5.36")
	req.Header.Set("Timezone", time.Now().Format("Mon Jan 02 2006 15:04:05 GMT-0700"))
	req.Header.Set("X-Request-Id", uuid.NewString())
	cookies := []string{"token=" + token}
	if c.cfg.SsxmodItna != "" {
		cookies = append(cookies, "ssxmod_itna="+c.cfg.SsxmodItna)
	}
	if c.cfg.Ssxmodi2 != "" {
		cookies = append(cookies, "ssxmod_itna2="+c.cfg.Ssxmodi2)
	}
	req.Header.Set("Cookie", strings.Join(cookies, "; "))

	pooling := c.cfg.PoolingEnabled
	if c.configRef != nil {
		pooling = c.configRef.Features.ConnectionPooling
	}
	if !pooling {
		req.Close = true
	}
}

// Models fetches the dynamic model list. Returns the raw JSON for direct
// passthrough plus a parsed view for the caller.
func (c *Client) Models(ctx context.Context, token string) (*ModelsResponse, error) {
	u, err := url.JoinPath(c.cfg.BaseURL, "/api/models")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req, token)

	resp, err := c.doWithFallback(ctx, req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &UpstreamError{Status: resp.StatusCode, Body: string(body)}
	}
	out := &ModelsResponse{}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	return out, nil
}

// NewChat allocates a chat_id by calling /api/v2/chats/new.
func (c *Client) NewChat(ctx context.Context, token, model, chatType string) (string, error) {
	body := NewChatRequest{
		Title:     "New Chat",
		Models:    []string{model},
		ChatMode:  "normal",
		ChatType:  chatType,
		Timestamp: time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	u, err := url.JoinPath(c.cfg.BaseURL, "/api/v2/chats/new")
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	c.applyHeaders(req, token)

	resp, err := c.doWithFallback(ctx, req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", &UpstreamError{Status: resp.StatusCode, Body: string(payload)}
	}
	out := NewChatResponse{}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", fmt.Errorf("decode new chat: %w", err)
	}
	if out.Data.ID == "" {
		return "", errors.New("upstream returned empty chat id")
	}
	return out.Data.ID, nil
}

// Completions opens a streaming POST to /api/v2/chat/completions. The caller
// must close the returned body. The request always asks for streaming because
// the upstream does not reliably support non-streaming responses.
func (c *Client) Completions(ctx context.Context, token string, req CompletionRequest) (io.ReadCloser, error) {
	req.Stream = true
	req.IncrementalOutput = true
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.JoinPath(c.cfg.BaseURL, "/api/v2/chat/completions")
	if err != nil {
		return nil, err
	}
	if req.ChatID != "" {
		endpoint += "?chat_id=" + url.QueryEscape(req.ChatID)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c.applyHeaders(httpReq, token)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.doStreamWithFallback(ctx, httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, &UpstreamError{Status: resp.StatusCode, Body: string(body)}
	}
	return resp.Body, nil
}

// UpstreamError captures a non-2xx response from chat.qwen.ai.
type UpstreamError struct {
	Status int
	Body   string
}

func (e *UpstreamError) Error() string {
	body := e.Body
	if len(body) > 256 {
		body = body[:256] + "..."
	}
	return fmt.Sprintf("upstream %d: %s", e.Status, body)
}

func isAntiBotResponse(resp *http.Response, body string) bool {
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return true
	case 503:
		ct := resp.Header.Get("Content-Type")
		if strings.Contains(ct, "text/html") {
			return true
		}
	}
	if body != "" {
		lower := strings.ToLower(body)
		if strings.Contains(lower, "challenge") || strings.Contains(lower, "captcha") ||
			strings.Contains(lower, "cloudflare") || strings.Contains(lower, "verify") {
			return true
		}
	}
	return false
}

func (c *Client) doWithFallback(ctx context.Context, req *http.Request) (*http.Response, error) {
	return c.doWithFallbackClient(ctx, req, c.http)
}

func (c *Client) doStreamWithFallback(ctx context.Context, req *http.Request) (*http.Response, error) {
	return c.doWithFallbackClient(ctx, req, c.stream)
}

func (c *Client) doWithFallbackClient(ctx context.Context, req *http.Request, client *http.Client) (*http.Response, error) {
	fallbackEnabled := c.cfg.BrowserFallbackEnabled
	if c.configRef != nil {
		fallbackEnabled = c.configRef.Features.BrowserEngineFallback
	}

	resp, err := client.Do(req)
	if err != nil || !fallbackEnabled || c.browser == nil {
		return resp, err
	}

	// Limit anti-bot body read to 1 MiB — these responses are typically small
	// HTML/JSON error pages. Unbounded reads waste memory on large upstream responses.
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	bodyStr := string(bodyBytes)

	if isAntiBotResponse(resp, bodyStr) {
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		browserResp, browserErr := c.browser.Do(ctx, req)
		if browserErr == nil {
			return browserResp, nil
		}
	}

	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return resp, err
}
