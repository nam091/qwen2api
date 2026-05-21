package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/keaume34/qwen2api/internal/config"
)

// --- System Status ---

type systemStatus struct {
	Uptime       string          `json:"uptime"`
	Tokens       []tokenStatus   `json:"tokens"`
	CacheEntries int             `json:"cache_entries"`
	Features     map[string]bool `json:"features"`
}

type tokenStatus struct {
	Name       string `json:"name,omitempty"`
	Value      string `json:"value"`
	OnCooldown bool   `json:"on_cooldown"`
	Hits       int64  `json:"hits"`
	Failures   int64  `json:"failures"`
}

func (h *handlers) adminStatus(w http.ResponseWriter, _ *http.Request) {
	poolStatuses := h.deps.TokenPool.Statuses()
	tokens := make([]tokenStatus, 0, len(poolStatuses))
	for _, s := range poolStatuses {
		tokens = append(tokens, tokenStatus{
			Name:       s.Name,
			Value:      maskToken(s.Value),
			OnCooldown: s.OnCooldown,
			Hits:       s.Hits,
			Failures:   s.Failures,
		})
	}

	cacheEntries := 0
	if h.deps.Cache != nil {
		cacheEntries, _, _ = h.deps.Cache.Stats()
	}

	status := systemStatus{
		Uptime:       time.Since(h.deps.startTime).Truncate(time.Second).String(),
		Tokens:       tokens,
		CacheEntries: cacheEntries,
		Features:     featureMap(h.deps.Config.Features),
	}
	writeJSON(w, http.StatusOK, status)
}

// --- Account Management ---

type accountInfo struct {
	Name     string `json:"name,omitempty"`
	Value    string `json:"value"`
	Hits     int64  `json:"hits"`
	Failures int64  `json:"failures"`
}

func (h *handlers) adminListAccounts(w http.ResponseWriter, _ *http.Request) {
	statuses := h.deps.TokenPool.Statuses()
	accounts := make([]accountInfo, 0, len(statuses))
	for _, s := range statuses {
		accounts = append(accounts, accountInfo{
			Name:     s.Name,
			Value:    maskToken(s.Value),
			Hits:     s.Hits,
			Failures: s.Failures,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

// --- Settings ---

type settingsResponse struct {
	AutoRefresh      bool `json:"auto_refresh"`
	RetryMaxAttempts int  `json:"retry_max_attempts"`
	TimeoutSeconds   int  `json:"timeout_seconds"`
	CooldownSeconds  int  `json:"cooldown_seconds"`
	CacheMaxEntries  int  `json:"cache_max_entries"`
	CacheTTLSeconds  int  `json:"cache_ttl_seconds"`
}

func (h *handlers) adminGetSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := h.deps.Config
	resp := settingsResponse{
		AutoRefresh:      cfg.Features.AutoTokenRefresh,
		RetryMaxAttempts: cfg.Retry.MaxAttempts,
		TimeoutSeconds:   cfg.TimeoutSeconds,
		CooldownSeconds:  cfg.CooldownSeconds,
		CacheMaxEntries:  cfg.Cache.MaxEntries,
		CacheTTLSeconds:  cfg.Cache.TTLSeconds,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handlers) adminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RetryMaxAttempts *int `json:"retry_max_attempts"`
		TimeoutSeconds   *int `json:"timeout_seconds"`
		CooldownSeconds  *int `json:"cooldown_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if body.RetryMaxAttempts != nil && *body.RetryMaxAttempts >= 0 && *body.RetryMaxAttempts <= 10 {
		h.deps.Config.Retry.MaxAttempts = *body.RetryMaxAttempts
	}
	if body.TimeoutSeconds != nil && *body.TimeoutSeconds > 0 {
		h.deps.Config.TimeoutSeconds = *body.TimeoutSeconds
	}
	if body.CooldownSeconds != nil && *body.CooldownSeconds >= 0 {
		h.deps.Config.CooldownSeconds = *body.CooldownSeconds
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// --- Helpers ---

func maskToken(t string) string {
	if len(t) <= 8 {
		return "****"
	}
	return t[:4] + "..." + t[len(t)-4:]
}

func featureMap(f config.FeatureToggles) map[string]bool {
	return map[string]bool{
		"prompt_caching":       f.PromptCaching,
		"retry_on_failure":     f.RetryOnTokenFailure,
		"connection_pooling":   f.ConnectionPooling,
		"auto_token_refresh":   f.AutoTokenRefresh,
		"metrics":              f.Metrics,
		"request_logging":      f.RequestLogging,
		"dashboard":            f.Dashboard,
		"multi_format_tools":   f.MultiFormatToolParsing,
		"multimodal":           f.Multimodal,
		"embeddings":           f.Embeddings,
		"topic_isolation":      f.TopicIsolation,
		"file_content_cache":   f.FileContentCache,
		"chat_gc":              f.ChatGC,
		"precise_tokens":       f.PreciseTokens,
		"client_profile":       f.ClientProfile,
		"tool_few_shot":        f.ToolFewShot,
		"session_affinity":     f.SessionAffinity,
		"per_account_proxy":    f.PerAccountProxy,
		"auto_login":           f.AutoLogin,
		"image_generation":     f.ImageGeneration,
		"gemini_api":           f.GeminiAPI,
		"admin_api":            f.AdminAPI,
		"file_upload_oss":      f.FileUploadOSS,
		"context_offloading":   f.ContextOffloading,
		"ssxmod_generation":    f.SSXMODGeneration,
		"browser_fingerprint":  f.BrowserFingerprint,
		"browser_fallback":     f.BrowserFallback,
	}
}
