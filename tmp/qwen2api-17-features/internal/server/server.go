// Package server wires the chi router, middleware, and OpenAI-compatible
// handlers for qwen2api.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/keaume34/qwen2api/internal/affinity"
	"github.com/keaume34/qwen2api/internal/config"
	"github.com/keaume34/qwen2api/internal/filecache"
	"github.com/keaume34/qwen2api/internal/garbagecollector"
	"github.com/keaume34/qwen2api/internal/metrics"
	"github.com/keaume34/qwen2api/internal/promptcache"
	"github.com/keaume34/qwen2api/internal/qwen"
	"github.com/keaume34/qwen2api/internal/reqlog"
	"github.com/keaume34/qwen2api/internal/ssxmod"
	"github.com/keaume34/qwen2api/internal/tokenpool"
)

// Deps holds the wired dependencies for the HTTP layer.
type Deps struct {
	Config    config.Config
	Logger    *slog.Logger
	Qwen      *qwen.Client
	TokenPool *tokenpool.Pool
	Cache     *promptcache.Cache
	Metrics   *metrics.Registry
	ReqLog    *reqlog.Logger

	// New feature dependencies
	FileCache      *filecache.Cache
	ChatGC         *garbagecollector.GC
	AffinityStore  *affinity.Store
	SSXMODManager  *ssxmod.Manager

	startTime time.Time
}

// New returns the configured http.Handler.
func New(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	deps.startTime = time.Now()
	h := &handlers{deps: deps}
	if deps.Metrics != nil {
		r.Use(h.metricsMiddleware)
	}

	r.Get("/healthz", h.health)
	r.Get("/readyz", h.ready)

	if deps.Config.Features.Metrics && deps.Metrics != nil {
		r.Get("/metrics", h.prometheusMetrics)
	}
	if deps.Config.Features.Dashboard {
		r.Get("/dashboard", h.dashboard)
		r.Get("/dashboard/data", h.dashboardData)
	}

	// OpenAI-compatible surface, accessible both at /v1/* and at the root for
	// clients that strip the version prefix.
	r.Group(func(r chi.Router) {
		r.Use(h.authMiddleware)
		r.Get("/v1/models", h.listModels)
		r.Get("/models", h.listModels)
		r.Post("/v1/chat/completions", h.chatCompletions)
		r.Post("/chat/completions", h.chatCompletions)
		if deps.Config.Features.Embeddings {
			r.Post("/v1/embeddings", h.embeddings)
			r.Post("/embeddings", h.embeddings)
		}
		// Claude API compatibility
		r.Post("/anthropic/v1/messages", h.claudeMessages)
	})

	if deps.Config.Features.APIKeyRotation || deps.Config.Features.AdminAPI {
		r.Group(func(r chi.Router) {
			r.Use(h.adminMiddleware)
			r.Get("/admin/keys", h.listAPIKeys)
			r.Post("/admin/keys", h.createAPIKey)
			r.Delete("/admin/keys/{value}", h.deleteAPIKey)
			if deps.Config.Features.AdminAPI {
				r.Get("/admin/status", h.adminStatus)
				r.Get("/admin/accounts", h.adminListAccounts)
				r.Get("/admin/settings", h.adminGetSettings)
				r.Put("/admin/settings", h.adminUpdateSettings)
			}
		})
	}

	// Image generation API (DALL-E compatible)
	if deps.Config.Features.ImageGeneration {
		r.Group(func(r chi.Router) {
			r.Use(h.authMiddleware)
			r.Post("/v1/images/generations", h.imageGenerations)
			r.Post("/images/generations", h.imageGenerations)
		})
	}

	// Gemini API
	if deps.Config.Features.GeminiAPI {
		r.Group(func(r chi.Router) {
			r.Use(h.authMiddleware)
			r.Get("/v1beta/models", h.geminiListModels)
			r.Post("/v1beta/models/{model}:generateContent", h.geminiGenerateContent)
			r.Post("/v1beta/models/{model}:streamGenerateContent", h.geminiStreamGenerateContent)
		})
	}

	return r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
