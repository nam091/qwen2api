package autologin

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestIsExpiringSoon(t *testing.T) {
	// Build a fake JWT with exp in 2 hours
	exp := time.Now().Add(2 * time.Hour).Unix()
	payload, _ := json.Marshal(map[string]any{"exp": exp})
	token := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"

	if IsExpiringSoon(token, 1) {
		t.Error("token expiring in 2h should not be 'expiring soon' with 1h threshold")
	}
	if !IsExpiringSoon(token, 3) {
		t.Error("token expiring in 2h should be 'expiring soon' with 3h threshold")
	}
}

func TestTokenRemainingHours(t *testing.T) {
	exp := time.Now().Add(5 * time.Hour).Unix()
	payload, _ := json.Marshal(map[string]any{"exp": exp})
	token := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"

	h := TokenRemainingHours(token)
	if h < 4 || h > 5 {
		t.Errorf("expected ~5 hours remaining, got %d", h)
	}
}

func TestTokenRemainingHoursInvalid(t *testing.T) {
	h := TokenRemainingHours("not-a-jwt")
	if h != -1 {
		t.Errorf("expected -1 for invalid token, got %d", h)
	}
}

func TestJwtExpiry(t *testing.T) {
	exp := int64(1700000000)
	payload, _ := json.Marshal(map[string]any{"exp": exp})
	token := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	got := jwtExpiry(token)
	if got != exp {
		t.Errorf("expected %d, got %d", exp, got)
	}
}
