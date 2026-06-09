// Package server wires the chi router, middleware, and OpenAI-compatible
// handlers for qwen2api.
package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/keaume34/qwen2api/internal/affinity"
	"github.com/keaume34/qwen2api/internal/config"
	"github.com/keaume34/qwen2api/internal/database"
	"github.com/keaume34/qwen2api/internal/filecache"
	"github.com/keaume34/qwen2api/internal/metrics"
	"github.com/keaume34/qwen2api/internal/ossupload"
	"github.com/keaume34/qwen2api/internal/promptcache"
	"github.com/keaume34/qwen2api/internal/qwen"
	"github.com/keaume34/qwen2api/internal/reqlog"
	"github.com/keaume34/qwen2api/internal/session"
	"github.com/keaume34/qwen2api/internal/tokencount"
	"github.com/keaume34/qwen2api/internal/tokenpool"
)

// Deps holds the wired dependencies for the HTTP layer.
type Deps struct {
	Config    *config.Config
	Logger    *slog.Logger
	Qwen      *qwen.Client
	TokenPool *tokenpool.Pool
	Cache     *promptcache.Cache
	Metrics   *metrics.Registry
	ReqLog    *reqlog.Logger

	// Phase 1 features
	Affinity      *affinity.Store
	FileCache     *filecache.Cache
	TokenCounter  *tokencount.Counter
	SessionStore  *session.Store
	TunnelManager interface {
		Start(port int) (string, error)
		Stop() error
		Status() (running bool, url string, port int)
	}
	ShutdownCh chan struct{}

	// Error handling and tracking
	ErrorHandler *ErrorHandler
	ErrorTracker *ErrorTracker

	// Database for conversation persistence
	Database *database.Store

	// Cookie store for dynamic cookie updates
	CookieStore *CookieStore
}

// New returns the configured http.Handler.
func New(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// Add error logging middleware
	if deps.ErrorHandler != nil {
		r.Use(ErrorLoggingMiddleware(deps.Logger))
	}

	h := &handlers{
		deps:          deps,
		imageUploader: ossupload.NewUploader(deps.Config.BaseURL, deps.Config.UserAgent, deps.Logger),
		imageCache:    newImageUploadCache(),
		sessionStore:  deps.SessionStore,
	}
	if deps.Metrics != nil {
		r.Use(h.metricsMiddleware)
	}

	r.Get("/healthz", h.health)
	r.Get("/readyz", h.ready)

	if deps.Metrics != nil {
		r.Get("/metrics", h.prometheusMetrics)
	}
	r.Get("/dashboard", h.dashboard)
	// NOTE: /dashboard/data moved inside adminMiddleware group below
	r.Get("/v1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "API key required for remote API access"})
	})
	r.Get("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "API key required for remote API access"})
	})

	// OpenAI-compatible surface, accessible both at /v1/* and at the root for
	// clients that strip the version prefix.
	r.Group(func(r chi.Router) {
		r.Use(h.authMiddleware)
		r.Get("/v1/models", h.listModels)
		r.Get("/v1/v1/models", h.listModels)
		r.Get("/models", h.listModels)
		r.Post("/v1/chat/completions", h.chatCompletions)
		r.Post("/chat/completions", h.chatCompletions)
		r.Post("/v1/images/generations", h.imageGenerations)
		r.Post("/images/generations", h.imageGenerations)
		r.Post("/v1/embeddings", h.embeddings)
		r.Post("/embeddings", h.embeddings)
		// OpenAI Responses API
		r.Post("/v1/responses", h.responses)
		r.Post("/responses", h.responses)
		// Claude API compatibility
		r.Post("/anthropic/v1/messages", h.claudeMessages)
		r.Post("/v1/messages", h.claudeMessages)
		r.Post("/v1/v1/messages", h.claudeMessages)
		r.Post("/messages", h.claudeMessages)
		// Claude Code count_tokens endpoint
		r.Post("/v1/messages/count_tokens", h.claudeCountTokens)
		r.Post("/messages/count_tokens", h.claudeCountTokens)
	})

	r.Group(func(r chi.Router) {
		r.Use(h.adminMiddleware)
		r.Get("/dashboard/data", h.dashboardData)
		r.Get("/admin/keys", h.listAPIKeys)
		r.Post("/admin/keys", h.createAPIKey)
		r.Delete("/admin/keys/{value}", h.deleteAPIKey)
	})

	// Config management API (admin only)
	r.Group(func(r chi.Router) {
		r.Use(h.adminMiddleware)
		r.Get("/admin/config", h.getConfig)
		r.Put("/admin/config/features", h.updateFeatures)
		r.Post("/admin/config/tokens", h.addToken)
		r.Post("/admin/config/tokens/test", h.testTokenValidity)
		r.Delete("/admin/config/tokens", h.removeToken)
		r.Put("/admin/config/aliases", h.updateModelAliases)
		r.Get("/admin/logs/stream", h.streamLogs)
		r.Post("/admin/models/test", h.testModel)
		r.Get("/admin/models/refresh", h.syncModelsHandler)
		r.Get("/admin/tunnel/status", h.getTunnelStatus)
		r.Post("/admin/tunnel/start", h.startTunnel)
		r.Post("/admin/tunnel/stop", h.stopTunnel)
		r.Post("/admin/shutdown", h.shutdownServer)

		// CLI Config management
		r.Get("/admin/cliconfig/status", h.getCliConfigStatus)
		r.Post("/admin/cliconfig/apply", h.applyCliConfig)
		r.Post("/admin/cliconfig/reset", h.resetCliConfig)

		// Error tracking API
		registerErrorRoutes(r, h)

		// Cookie management API
		registerCookieRoutes(r, h)
	})

	return r
}

// allowedHeaders is the fixed whitelist of headers accepted in CORS preflight.
// Reflecting arbitrary request headers is a security risk and prevents
// preflight caching because the response varies per request.
const allowedHeaders = "Authorization, Content-Type, Accept, X-API-Key, X-Request-Id, Anthropic-Version, Anthropic-Beta, Anthropic-Dangerous-Direct-Browser-Access"

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS, PUT")
		w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
		w.Header().Set("Access-Control-Max-Age", "86400") // Cache preflight for 24h

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
