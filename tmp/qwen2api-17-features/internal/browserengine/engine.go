// Package browserengine provides a hybrid HTTP engine that normally uses the
// standard Go HTTP client but can fall back to a headless browser when the
// upstream detects bot-like traffic (WAF blocks, 403, 429). The browser
// engine uses Chrome DevTools Protocol (CDP) to execute requests.
//
// In Go, true browser automation requires external dependencies (chromedp,
// playwright). This package provides the abstraction layer and fallback logic;
// the actual browser implementation can be plugged in via the BrowserClient
// interface.
package browserengine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// ErrAllEnginesFailed is returned when both HTTP and browser engines fail.
var ErrAllEnginesFailed = errors.New("all engines failed")

// BrowserClient is the interface for headless browser requests.
// Implement this with chromedp, rod, or playwright-go.
type BrowserClient interface {
	// Do executes a request through the headless browser.
	Do(ctx context.Context, method, url string, body io.Reader, headers map[string]string) (*BrowserResponse, error)
	// Close shuts down the browser instance.
	Close() error
}

// BrowserResponse holds the browser's response.
type BrowserResponse struct {
	StatusCode int
	Body       io.ReadCloser
	Headers    http.Header
}

// Mode describes which engine to prefer.
type Mode int

const (
	HTTPFirst   Mode = iota // try HTTP, fall back to browser
	BrowserFirst            // try browser, fall back to HTTP
)

// HybridEngine routes requests between HTTP client and browser.
type HybridEngine struct {
	httpClient *http.Client
	browser    BrowserClient
	logger     *slog.Logger
	mode       Mode

	// Stats
	httpHits    atomic.Int64
	browserHits atomic.Int64
	fallbacks   atomic.Int64
}

// NewHybridEngine creates a hybrid engine.
func NewHybridEngine(httpClient *http.Client, browser BrowserClient, logger *slog.Logger, mode Mode) *HybridEngine {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 120 * time.Second}
	}
	return &HybridEngine{
		httpClient: httpClient,
		browser:    browser,
		logger:     logger,
		mode:       mode,
	}
}

// Do executes a request using the preferred engine, falling back if needed.
func (e *HybridEngine) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	switch e.mode {
	case BrowserFirst:
		return e.browserFirstDo(ctx, req)
	default:
		return e.httpFirstDo(ctx, req)
	}
}

func (e *HybridEngine) httpFirstDo(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := e.httpClient.Do(req)
	if err == nil && !isBlockResponse(resp) {
		e.httpHits.Add(1)
		return resp, nil
	}

	if err != nil {
		e.logger.Warn("http request failed, trying browser", "error", err)
	} else {
		e.logger.Warn("http request blocked, trying browser", "status", resp.StatusCode)
		_ = resp.Body.Close()
	}

	return e.browserDo(ctx, req)
}

func (e *HybridEngine) browserFirstDo(ctx context.Context, req *http.Request) (*http.Response, error) {
	if e.browser == nil {
		return e.httpClient.Do(req)
	}

	resp, err := e.browserDo(ctx, req)
	if err == nil {
		return resp, nil
	}

	e.logger.Warn("browser request failed, trying http", "error", err)
	e.fallbacks.Add(1)
	return e.httpClient.Do(req)
}

func (e *HybridEngine) browserDo(ctx context.Context, req *http.Request) (*http.Response, error) {
	if e.browser == nil {
		return nil, errors.New("browser engine not available")
	}

	headers := make(map[string]string)
	for k, vs := range req.Header {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}

	bResp, err := e.browser.Do(ctx, req.Method, req.URL.String(), req.Body, headers)
	if err != nil {
		e.fallbacks.Add(1)
		return nil, err
	}

	e.browserHits.Add(1)

	// Convert BrowserResponse to http.Response
	resp := &http.Response{
		StatusCode: bResp.StatusCode,
		Body:       bResp.Body,
		Header:     bResp.Headers,
	}
	return resp, nil
}

// Stats returns engine usage statistics.
func (e *HybridEngine) Stats() EngineStats {
	return EngineStats{
		HTTPHits:    e.httpHits.Load(),
		BrowserHits: e.browserHits.Load(),
		Fallbacks:   e.fallbacks.Load(),
	}
}

// EngineStats holds usage counters.
type EngineStats struct {
	HTTPHits    int64 `json:"http_hits"`
	BrowserHits int64 `json:"browser_hits"`
	Fallbacks   int64 `json:"fallbacks"`
}

// isBlockResponse detects WAF/anti-bot blocks.
func isBlockResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusUnauthorized, http.StatusTooManyRequests:
		return true
	case 503:
		// Check for WAF challenge pages
		ct := resp.Header.Get("Content-Type")
		if strings.Contains(ct, "text/html") {
			return true
		}
	}
	return false
}

// Close shuts down the browser if available.
func (e *HybridEngine) Close() error {
	if e.browser != nil {
		return e.browser.Close()
	}
	return nil
}
