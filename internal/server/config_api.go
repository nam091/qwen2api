package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/keaume34/qwen2api/internal/cliconfig"
	"github.com/keaume34/qwen2api/internal/config"
)

func (h *handlers) saveConfig() error {
	path := os.Getenv("QWEN2API_CONFIG_PATH")
	if path == "" {
		path = "config.json"
	}
	data, err := json.MarshalIndent(h.deps.Config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

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

// addToken adds new Qwen token(s) to the pool (admin only). Supports single token, array of tokens, or bulk tokens container.
func (h *handlers) addToken(w http.ResponseWriter, r *http.Request) {
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "failed to read body: "+err.Error())
		return
	}

	var tokensToAdd []config.Token

	// Try 1: Single Token
	var single config.Token
	if err := json.Unmarshal(rawBody, &single); err == nil && single.Value != "" {
		tokensToAdd = append(tokensToAdd, single)
	} else {
		// Try 2: Array of tokens
		var arr []config.Token
		if err := json.Unmarshal(rawBody, &arr); err == nil && len(arr) > 0 {
			tokensToAdd = arr
		} else {
			// Try 3: Object with "tokens" field
			var container struct {
				Tokens []config.Token `json:"tokens"`
			}
			if err := json.Unmarshal(rawBody, &container); err == nil && len(container.Tokens) > 0 {
				tokensToAdd = container.Tokens
			}
		}
	}

	if len(tokensToAdd) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "no valid token values provided")
		return
	}

	// Validate and append to Config and update active pool
	var added []config.Token
	for _, t := range tokensToAdd {
		t.Value = strings.TrimSpace(t.Value)
		if t.Value == "" {
			continue
		}
		t.Name = strings.TrimSpace(t.Name)
		h.deps.Config.Tokens = append(h.deps.Config.Tokens, t)
		added = append(added, t)
	}

	// Dynamic update in-memory active pool!
	h.deps.TokenPool.SetTokens(h.deps.Config.Tokens)

	// Save to config.json!
	if err := h.saveConfig(); err != nil {
		h.deps.Logger.Error("failed to save config to file", "err", err)
	}

	h.deps.Logger.Info("tokens added via API", "count", len(added))
	writeJSON(w, http.StatusCreated, map[string]any{
		"success": true,
		"added":   len(added),
		"total":   len(h.deps.Config.Tokens),
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

	// Dynamic update in-memory active pool!
	h.deps.TokenPool.SetTokens(h.deps.Config.Tokens)

	// Save to config.json!
	if err := h.saveConfig(); err != nil {
		h.deps.Logger.Error("failed to save config to file", "err", err)
	}

	h.deps.Logger.Info("token removed via API")
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"total": len(h.deps.Config.Tokens),
	})
}

// testTokenValidity checks if a given Qwen token is valid by querying the upstream Models list (admin only).
func (h *handlers) testTokenValidity(w http.ResponseWriter, r *http.Request) {
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

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	_, err := h.deps.Qwen.Models(ctx, body.Value)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": false,
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"valid": true,
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

// getCliConfigStatus returns the status of supported CLI tools (admin only).
func (h *handlers) getCliConfigStatus(w http.ResponseWriter, _ *http.Request) {
	claude, err := cliconfig.CheckClaude()
	if err != nil {
		h.deps.Logger.Error("check claude status", "err", err)
	}
	cline, err := cliconfig.CheckCline()
	if err != nil {
		h.deps.Logger.Error("check cline status", "err", err)
	}
	deepseek, err := cliconfig.CheckDeepSeek()
	if err != nil {
		h.deps.Logger.Error("check deepseek status", "err", err)
	}
	qwen, err := cliconfig.CheckQwen()
	if err != nil {
		h.deps.Logger.Error("check qwen status", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"claude":   claude,
		"cline":    cline,
		"deepseek": deepseek,
		"qwen":     qwen,
	})
}

// applyCliConfig configures a CLI tool to route requests to Qwen2API (admin only).
func (h *handlers) applyCliConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tool        string `json:"tool"`
		BaseURL     string `json:"base_url"`
		APIKey      string `json:"api_key"`
		Model       string `json:"model"`
		SonnetModel string `json:"sonnet_model"`
		OpusModel   string `json:"opus_model"`
		HaikuModel  string `json:"haiku_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	if body.Tool == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "tool is required")
		return
	}

	baseURL := body.BaseURL
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%d", h.deps.Config.Port)
	}

	apiKey := body.APIKey
	if strings.Contains(apiKey, "...") {
		for _, k := range h.deps.Config.APIKeys {
			masked := k.Value
			if len(masked) > 8 {
				masked = masked[:4] + "..." + masked[len(masked)-4:]
			}
			if masked == apiKey {
				apiKey = k.Value
				break
			}
		}
	}

	var err error
	switch strings.ToLower(body.Tool) {
	case "claude":
		err = cliconfig.ApplyClaude(baseURL, apiKey, body.SonnetModel, body.OpusModel, body.HaikuModel)
	case "cline":
		err = cliconfig.ApplyCline(baseURL, apiKey, body.Model)
	case "deepseek":
		err = cliconfig.ApplyDeepSeek(baseURL, apiKey, body.Model)
	case "qwen":
		err = cliconfig.ApplyQwen(baseURL, apiKey, body.Model)
	default:
		writeError(w, http.StatusBadRequest, "invalid_request_error", "unsupported tool: "+body.Tool)
		return
	}

	if err != nil {
		h.deps.Logger.Error("apply cli config failed", "tool", body.Tool, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to apply config: "+err.Error())
		return
	}

	h.deps.Logger.Info("cli config applied successfully", "tool", body.Tool)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Config applied successfully for " + body.Tool,
	})
}

// resetCliConfig resets a CLI tool config to defaults (admin only).
func (h *handlers) resetCliConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tool string `json:"tool"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	if body.Tool == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "tool is required")
		return
	}

	var err error
	switch strings.ToLower(body.Tool) {
	case "claude":
		err = cliconfig.ResetClaude()
	case "cline":
		err = cliconfig.ResetCline()
	case "deepseek":
		err = cliconfig.ResetDeepSeek()
	case "qwen":
		err = cliconfig.ResetQwen()
	default:
		writeError(w, http.StatusBadRequest, "invalid_request_error", "unsupported tool: "+body.Tool)
		return
	}

	if err != nil {
		h.deps.Logger.Error("reset cli config failed", "tool", body.Tool, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to reset config: "+err.Error())
		return
	}

	h.deps.Logger.Info("cli config reset successfully", "tool", body.Tool)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Config reset successfully for " + body.Tool,
	})
}
