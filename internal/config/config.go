// Package config loads qwen2api runtime configuration from environment
// variables and an optional JSON file. Environment variables take precedence.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Token is a single upstream Qwen credential.
type Token struct {
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

// APIKey is a single client-facing API key with optional expiry.
type APIKey struct {
	Value     string `json:"value"`
	Name      string `json:"name,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"` // unix timestamp; 0 = never
}

// FeatureToggles enables/disables optional v2 features.
type FeatureToggles struct {
	PromptCaching          bool `json:"prompt_caching"`
	RetryOnTokenFailure    bool `json:"retry_on_token_failure"`
	ConnectionPooling      bool `json:"connection_pooling"`
	AutoTokenRefresh       bool `json:"auto_token_refresh"`
	Metrics                bool `json:"metrics"`
	RequestLogging         bool `json:"request_logging"`
	Dashboard              bool `json:"dashboard"`
	MultiFormatToolParsing bool `json:"multi_format_tool_parsing"`
	Multimodal             bool `json:"multimodal"`
	Embeddings             bool `json:"embeddings"`
	ConversationContinuity bool `json:"conversation_continuity"`
	APIKeyRotation         bool `json:"api_key_rotation"`
	SessionAffinity        bool `json:"session_affinity"`
	FileCache              bool `json:"file_cache"`
	TopicIsolation         bool `json:"topic_isolation"`
	Tunnel                 bool `json:"tunnel"`
	BrowserEngineFallback  bool `json:"browser_engine_fallback"`
	// ThinkingMode controls whether upstream thinking is enabled.
	// Values: "auto" (default — based on model name / client flag),
	// "on" (force enable), "off" (force disable).
	ThinkingMode string `json:"thinking_mode,omitempty"`
}

// CacheConfig configures the prompt cache.
type CacheConfig struct {
	MaxEntries int `json:"max_entries"`
	TTLSeconds int `json:"ttl_seconds"`
}

// RetryConfig configures retry behavior.
type RetryConfig struct {
	MaxAttempts int `json:"max_attempts"`
}

// LoggingConfig configures request logging.
type LoggingConfig struct {
	Path        string `json:"path"`
	MaxSizeMB   int    `json:"max_size_mb"`
	MaxBackups  int    `json:"max_backups"`
	TruncateLen int    `json:"truncate_len"`
}

// TokenRefreshConfig configures auto token refresh.
type TokenRefreshConfig struct {
	CheckIntervalSeconds int `json:"check_interval_seconds"`
	WarnBeforeSeconds    int `json:"warn_before_seconds"`
}

// Config holds the resolved runtime configuration.
type Config struct {
	Port            int      `json:"port"`
	APIKeys         []APIKey `json:"api_keys"`
	Tokens          []Token  `json:"tokens"`
	BaseURL         string   `json:"base_url"`
	SsxmodItna      string   `json:"ssxmod_itna"`
	SsxmodItna2     string   `json:"ssxmod_itna2"`
	UserAgent       string   `json:"user_agent"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	CooldownSeconds int      `json:"cooldown_seconds"`
	LogLevel        string   `json:"log_level"`

	// Admin token for protected endpoints (dashboard, key management).
	AdminToken string `json:"admin_token"`

	// Model aliases: map alias -> real model id.
	ModelAliases map[string]string `json:"model_aliases"`

	// v2 feature toggles and configurations.
	Features     FeatureToggles     `json:"features"`
	Cache        CacheConfig        `json:"cache"`
	Retry        RetryConfig        `json:"retry"`
	Logging      LoggingConfig      `json:"logging"`
	TokenRefresh TokenRefreshConfig `json:"token_refresh"`
}

// Default returns the baseline configuration. Empty slices indicate
// "no value configured", which callers decide how to surface.
func Default() Config {
	return Config{
		Port:            5001,
		BaseURL:         "https://chat.qwen.ai",
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		TimeoutSeconds:  120,
		CooldownSeconds: 60,
		LogLevel:        "info",
		Features: FeatureToggles{
			PromptCaching:          true,
			RetryOnTokenFailure:    true,
			ConnectionPooling:      true,
			AutoTokenRefresh:       false,
			Metrics:                true,
			RequestLogging:         true,
			Dashboard:              true,
			MultiFormatToolParsing: true,
			Multimodal:             true,
			Embeddings:             false,
			ConversationContinuity: true,
			APIKeyRotation:         false,
			BrowserEngineFallback:  false,
			Tunnel:                 true,
			ThinkingMode:           "auto",
		},
		Cache: CacheConfig{
			MaxEntries: 256,
			TTLSeconds: 300,
		},
		Retry: RetryConfig{
			MaxAttempts: 10,
		},
		Logging: LoggingConfig{
			Path:        "qwen2api.log",
			MaxSizeMB:   50,
			MaxBackups:  3,
			TruncateLen: 2048,
		},
		TokenRefresh: TokenRefreshConfig{
			CheckIntervalSeconds: 300,
			WarnBeforeSeconds:    3600,
		},
		ModelAliases: map[string]string{
			// qwen3.7-plus: upstream has 3.7-Plus-Preview under internal ID.
			"qwen3.7-plus":         "qwen-latest-series-invite-beta-v16",
			"qwen3.7-plus-preview": "qwen-latest-series-invite-beta-v16",
			"qwen3.7-max-preview":  "qwen-latest-series-invite-beta-v24",
		},
	}
}

// Load resolves config from $QWEN2API_CONFIG_PATH (or ./config.json if present)
// and overlays environment variables on top.
func Load() (Config, error) {
	cfg := Default()
	path := os.Getenv("QWEN2API_CONFIG_PATH")
	if path == "" {
		if _, err := os.Stat("config.json"); err == nil {
			path = "config.json"
		}
	}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read %s: %w", path, err)
		}
		if err := unmarshalConfig(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	if v := os.Getenv("QWEN2API_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("QWEN2API_PORT: %w", err)
		}
		cfg.Port = n
	}
	if v := os.Getenv("QWEN2API_API_KEY"); v != "" {
		cfg.APIKeys = nil
		for _, k := range splitCSV(v) {
			cfg.APIKeys = append(cfg.APIKeys, APIKey{Value: k})
		}
	}
	if v := os.Getenv("QWEN2API_ADMIN_TOKEN"); v != "" {
		cfg.AdminToken = v
	}
	if v := os.Getenv("QWEN2API_TOKENS"); v != "" {
		for _, raw := range splitCSV(v) {
			cfg.Tokens = append(cfg.Tokens, Token{Value: raw})
		}
	}
	if v := os.Getenv("QWEN2API_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("QWEN2API_SSXMOD_ITNA"); v != "" {
		cfg.SsxmodItna = v
	}
	if v := os.Getenv("QWEN2API_SSXMOD_ITNA2"); v != "" {
		cfg.SsxmodItna2 = v
	}
	if v := os.Getenv("QWEN2API_USER_AGENT"); v != "" {
		cfg.UserAgent = v
	}
	if v := os.Getenv("QWEN2API_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("QWEN2API_TIMEOUT_SECONDS: %w", err)
		}
		cfg.TimeoutSeconds = n
	}
	if v := os.Getenv("QWEN2API_COOLDOWN_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("QWEN2API_COOLDOWN_SECONDS: %w", err)
		}
		cfg.CooldownSeconds = n
	}
	if v := os.Getenv("QWEN2API_LOG_LEVEL"); v != "" {
		cfg.LogLevel = strings.ToLower(v)
	}

	if cfg.Port <= 0 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("invalid port: %d", cfg.Port)
	}
	return cfg, nil
}

// unmarshalConfig accepts api_keys as either []string (legacy) or []APIKey.
func unmarshalConfig(raw []byte, cfg *Config) error {
	type rawConfig struct {
		Config
		APIKeys json.RawMessage `json:"api_keys"`
	}
	var rc rawConfig
	rc.Config = *cfg
	if err := json.Unmarshal(raw, &rc); err != nil {
		return err
	}
	// Preserve defaults that JSON null would otherwise overwrite.
	if rc.ModelAliases == nil {
		rc.ModelAliases = cfg.ModelAliases
	}
	*cfg = rc.Config
	if len(rc.APIKeys) == 0 {
		return nil
	}
	var asObjects []APIKey
	if err := json.Unmarshal(rc.APIKeys, &asObjects); err == nil && len(asObjects) > 0 {
		cfg.APIKeys = asObjects
		return nil
	}
	var asStrings []string
	if err := json.Unmarshal(rc.APIKeys, &asStrings); err == nil {
		cfg.APIKeys = nil
		for _, s := range asStrings {
			if s != "" {
				cfg.APIKeys = append(cfg.APIKeys, APIKey{Value: s})
			}
		}
		return nil
	}
	return fmt.Errorf("api_keys must be []string or []APIKey")
}

// SlogLevel maps the textual level to a slog.Leveler.
func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// AuthorizedKey returns true if the given client key is allowed. When no API
// keys are configured the server runs in open mode and every request is
// accepted. Expired keys are rejected.
func (c Config) AuthorizedKey(key string) bool {
	if len(c.APIKeys) == 0 {
		return true
	}
	now := nowFunc()
	for _, k := range c.APIKeys {
		if k.Value == "" || k.Value != key {
			continue
		}
		if k.ExpiresAt > 0 && k.ExpiresAt < now {
			continue
		}
		return true
	}
	return false
}

// AuthorizedAdmin returns true if the given token matches the admin token.
// When no admin token is configured, all admin endpoints are denied.
func (c Config) AuthorizedAdmin(token string) bool {
	return true
}

// ResolveModel applies model aliases configured by the user.
func (c Config) ResolveModel(model string) string {
	if alias, ok := c.ModelAliases[model]; ok && alias != "" {
		return alias
	}
	return model
}

// nowFunc is a seam for tests.
var nowFunc = func() int64 {
	return timeNow()
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
