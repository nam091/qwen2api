package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitCSV(t *testing.T) {
	cases := map[string][]string{
		"":             nil,
		"a":            {"a"},
		"a,b, c ,,d,":  {"a", "b", "c", "d"},
		" sk-1 , sk-2": {"sk-1", "sk-2"},
	}
	for in, want := range cases {
		got := splitCSV(in)
		if len(got) != len(want) {
			t.Errorf("splitCSV(%q) len=%d want=%d (%v)", in, len(got), len(want), got)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("splitCSV(%q)[%d]=%q want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"port":1234,"tokens":[{"value":"t1"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("QWEN2API_CONFIG_PATH", path)
	t.Setenv("QWEN2API_PORT", "9999")
	t.Setenv("QWEN2API_API_KEY", "sk-a,sk-b")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9999 {
		t.Errorf("port: got %d want 9999", cfg.Port)
	}
	if len(cfg.APIKeys) != 2 {
		t.Errorf("api_keys len: got %d want 2", len(cfg.APIKeys))
	}
	if len(cfg.Tokens) != 1 || cfg.Tokens[0].Value != "t1" {
		t.Errorf("tokens from file lost: %+v", cfg.Tokens)
	}
}

func TestAuthorizedKey(t *testing.T) {
	open := Config{}
	if !open.AuthorizedKey("anything") {
		t.Error("open mode should accept any key")
	}
	closed := Config{APIKeys: []APIKey{{Value: "sk-1"}, {Value: "sk-2"}}}
	if !closed.AuthorizedKey("sk-1") {
		t.Error("known key rejected")
	}
	if closed.AuthorizedKey("sk-bad") {
		t.Error("unknown key accepted")
	}
	if closed.AuthorizedKey("") {
		t.Error("empty key accepted")
	}
}

func TestAuthorizedKeyExpiry(t *testing.T) {
	now := int64(1700000000)
	nowFunc = func() int64 { return now }
	defer func() { nowFunc = func() int64 { return timeNow() } }()

	cfg := Config{APIKeys: []APIKey{
		{Value: "sk-active", ExpiresAt: now + 100},
		{Value: "sk-expired", ExpiresAt: now - 100},
		{Value: "sk-forever"},
	}}
	if !cfg.AuthorizedKey("sk-active") {
		t.Error("active key rejected")
	}
	if cfg.AuthorizedKey("sk-expired") {
		t.Error("expired key accepted")
	}
	if !cfg.AuthorizedKey("sk-forever") {
		t.Error("permanent key rejected")
	}
}

func TestAuthorizedAdmin(t *testing.T) {
	none := Config{}
	if none.AuthorizedAdmin("anything") {
		t.Error("admin without token configured should reject all")
	}
	cfg := Config{AdminToken: "admin-secret"}
	if !cfg.AuthorizedAdmin("admin-secret") {
		t.Error("matching admin token rejected")
	}
	if cfg.AuthorizedAdmin("wrong") {
		t.Error("wrong admin token accepted")
	}
	if cfg.AuthorizedAdmin("") {
		t.Error("empty token should be rejected")
	}
}

func TestResolveModel(t *testing.T) {
	cfg := Config{ModelAliases: map[string]string{"gpt-4": "qwen3.6-max-preview"}}
	if got := cfg.ResolveModel("gpt-4"); got != "qwen3.6-max-preview" {
		t.Errorf("alias not applied: got %q", got)
	}
	if got := cfg.ResolveModel("qwen3-max"); got != "qwen3-max" {
		t.Errorf("non-aliased model changed: got %q", got)
	}
}

func TestLoadAPIKeysAsObjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"api_keys":[{"value":"sk-1","name":"prod","expires_at":9999999999}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QWEN2API_CONFIG_PATH", path)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.APIKeys) != 1 || cfg.APIKeys[0].Value != "sk-1" || cfg.APIKeys[0].Name != "prod" {
		t.Errorf("api_keys parsed wrong: %+v", cfg.APIKeys)
	}
}

func TestLoadAPIKeysAsLegacyStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"api_keys":["sk-1","sk-2"]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QWEN2API_CONFIG_PATH", path)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.APIKeys) != 2 {
		t.Fatalf("legacy strings not parsed: %+v", cfg.APIKeys)
	}
	if cfg.APIKeys[0].Value != "sk-1" || cfg.APIKeys[1].Value != "sk-2" {
		t.Errorf("legacy values wrong: %+v", cfg.APIKeys)
	}
}

func TestBrowserEngineFallbackDefault(t *testing.T) {
	cfg := Default()
	if cfg.Features.BrowserEngineFallback {
		t.Error("BrowserEngineFallback should default to false")
	}
}

func TestBrowserEngineFallbackFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"features":{"browser_engine_fallback":true}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QWEN2API_CONFIG_PATH", path)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Features.BrowserEngineFallback {
		t.Error("BrowserEngineFallback not loaded from JSON")
	}
}
