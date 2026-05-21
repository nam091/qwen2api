package server

import (
	"encoding/json"
	"net/http"

	"github.com/keaume34/qwen2api/internal/config"
)

// getConfig returns the current configuration (admin only).
func (h *handlers) getConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.deps.Config)
}

// updateFeatures updates feature toggles (admin only).
func (h *handlers) updateFeatures(w http.ResponseWriter, r *http.Request) {
	var body config.FeatureToggles
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	h.deps.Config.Features = body
	h.deps.Logger.Info("features updated via API", "features", body)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"features": body,
	})
}

// addToken adds a new Qwen token to the pool (admin only).
func (h *handlers) addToken(w http.ResponseWriter, r *http.Request) {
	var body config.Token
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	if body.Value == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "token value is required")
		return
	}

	h.deps.Config.Tokens = append(h.deps.Config.Tokens, body)
	h.deps.Logger.Info("token added via API", "name", body.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"success": true,
		"total": len(h.deps.Config.Tokens),
	})
}

// removeToken removes a token by value (admin only).
func (h *handlers) removeToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	if body.Value == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "token value is required")
		return
	}

	newTokens := make([]config.Token, 0, len(h.deps.Config.Tokens))
	found := false
	for _, t := range h.deps.Config.Tokens {
		if t.Value == body.Value {
			found = true
			continue
		}
		newTokens = append(newTokens, t)
	}

	if !found {
		writeError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}

	h.deps.Config.Tokens = newTokens
	h.deps.Logger.Info("token removed via API")
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"total": len(h.deps.Config.Tokens),
	})
}

// updateModelAliases updates model aliases (admin only).
func (h *handlers) updateModelAliases(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	h.deps.Config.ModelAliases = body
	h.deps.Logger.Info("model aliases updated via API", "count", len(body))
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"aliases": body,
	})
}
