package server

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/qwen"
	"github.com/keaume34/qwen2api/internal/session"
)

// staticModels is the fallback model list used when the upstream /api/models
// call is unavailable (e.g. no token configured yet).
// Updated from upstream as of 2026-05-23.
var staticModels = []string{
	"qwen3.7-max",
	"qwen-latest-series-invite-beta-v24", // Qwen3.7-Max-Preview
	"qwen-latest-series-invite-beta-v16", // Qwen3.7-Plus-Preview
	"qwen3.6-plus",
	"qwen3.6-max-preview",
	"qwen3.6-27b",
	"qwen3.6-35b-a3b",
	"qwen3.6-plus-preview",
	"qwen3.5-plus",
	"qwen3.5-omni-plus",
	"qwen3.5-flash",
	"qwen3.5-omni-flash",
	"qwen3.5-max-2026-03-08",
	"qwen3.5-397b-a17b",
	"qwen3.5-122b-a10b",
	"qwen3.5-35b-a3b",
	"qwen3.5-27b",
	"qwen3-max-2026-01-23",
	"qwen-plus-2025-07-28",
	"qwen3-coder-plus",
	"qwen3-vl-plus",
	"qwen3-omni-flash-2025-12-01",
	"qwen-max-latest",
}

// syncedModels holds models fetched via /admin/models/refresh (thread-safe).
var syncedModels atomic.Value

func init() {
	syncedModels.Store([]string(nil))
}

// refreshModels fetches the latest model list from upstream and stores it.
// Returns the fetched list (or nil on failure).
func (h *handlers) refreshModels() []string {
	token, err := h.deps.TokenPool.Take()
	if err != nil {
		return nil
	}
	resp, err := h.deps.Qwen.Models(context.Background(), token.Value)
	if err != nil {
		if _, ok := err.(*qwen.UpstreamError); ok {
			h.deps.TokenPool.MarkBad(token.Value)
		}
		return nil
	}
	ids := make([]string, 0, len(resp.Data))
	seen := map[string]bool{}
	for _, m := range resp.Data {
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		ids = append(ids, m.ID)
	}
	if len(ids) > 0 {
		syncedModels.Store(ids)
	}
	return ids
}


// modelContextLength returns the context window size for a model ID.
func modelContextLength(modelID string) int {
	return session.GetContextWindow(modelID, 32768)
}

func (h *handlers) listModels(w http.ResponseWriter, r *http.Request) {
	now := unixNow()
	out := openai.ModelList{Object: "list"}

	// Priority 1: synced model list (from manual /admin/models/refresh).
	if cached, ok := syncedModels.Load().([]string); ok && len(cached) > 0 {
		for _, id := range cached {
			out.Data = append(out.Data, openai.Model{
				ID:            id,
				Object:        "model",
				Created:       now,
				OwnedBy:       "qwen",
				ContextLength: modelContextLength(id),
			})
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Priority 2: live fetch from upstream.
	token, err := h.deps.TokenPool.Take()
	if err == nil {
		resp, errModels := h.deps.Qwen.Models(r.Context(), token.Value)
		if errModels == nil {
			seen := map[string]bool{}
			for _, m := range resp.Data {
				if m.ID == "" || seen[m.ID] {
					continue
				}
				seen[m.ID] = true
				out.Data = append(out.Data, openai.Model{
					ID:      m.ID,
					Object:  "model",
					Created: now,
					OwnedBy: "qwen",
				})
			}
		} else {
			h.deps.Logger.Warn("upstream models fetch failed; falling back to static list", "err", errModels)
			if _, ok := errModels.(*qwen.UpstreamError); ok {
				h.deps.TokenPool.MarkBad(token.Value)
			}
		}
	}

	// Priority 3: static fallback list.
	if len(out.Data) == 0 {
		for _, id := range staticModels {
			out.Data = append(out.Data, openai.Model{
				ID:            id,
				Object:        "model",
				Created:       now,
				OwnedBy:       "qwen",
				ContextLength: modelContextLength(id),
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// syncModelsHandler fetches the latest model list from upstream and stores it.
// GET /admin/models/refresh
func (h *handlers) syncModelsHandler(w http.ResponseWriter, r *http.Request) {
	ids := h.refreshModels()
	if ids == nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "failed to fetch models from upstream")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   len(ids),
		"models":  ids,
	})
}
