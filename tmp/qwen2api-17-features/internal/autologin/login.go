// Package autologin handles automatic token acquisition by logging in with
// email/password, JWT validation, and periodic refresh of expiring tokens.
package autologin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Credentials holds email + password for auto-login.
type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResult holds the result of a login attempt.
type LoginResult struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"` // unix
}

// Manager handles login and token refresh.
type Manager struct {
	baseURL   string
	client    *http.Client
	logger    *slog.Logger
	userAgent string
}

// NewManager creates a login manager.
func NewManager(baseURL, userAgent string, logger *slog.Logger) *Manager {
	if baseURL == "" {
		baseURL = "https://chat.qwen.ai"
	}
	return &Manager{
		baseURL:   strings.TrimRight(baseURL, "/"),
		client:    &http.Client{Timeout: 15 * time.Second},
		logger:    logger,
		userAgent: userAgent,
	}
}

// Login authenticates with email/password and returns a JWT token.
func (m *Manager) Login(ctx context.Context, creds Credentials) (*LoginResult, error) {
	if creds.Email == "" || creds.Password == "" {
		return nil, errors.New("email and password required")
	}

	pwHash := sha256Hex(creds.Password)
	body, _ := json.Marshal(map[string]string{
		"email":    creds.Email,
		"password": pwHash,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.baseURL+"/api/v1/auths/signin", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", m.userAgent)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read login response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("login failed (%d): %s", resp.StatusCode, truncate(string(payload), 256))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("decode login response: %w", err)
	}
	if result.Token == "" {
		return nil, errors.New("login returned empty token")
	}

	exp := jwtExpiry(result.Token)
	m.logger.Info("login success", "email", creds.Email, "expires_in_hours", hoursUntil(exp))
	return &LoginResult{Token: result.Token, ExpiresAt: exp}, nil
}

// IsExpiringSoon checks if a token will expire within the threshold.
func IsExpiringSoon(token string, thresholdHours int) bool {
	if thresholdHours <= 0 {
		thresholdHours = 6
	}
	exp := jwtExpiry(token)
	if exp <= 0 {
		return true
	}
	return time.Until(time.Unix(exp, 0)).Hours() < float64(thresholdHours)
}

// TokenRemainingHours returns hours until expiry (-1 if invalid).
func TokenRemainingHours(token string) int {
	exp := jwtExpiry(token)
	if exp <= 0 {
		return -1
	}
	h := int(time.Until(time.Unix(exp, 0)).Hours())
	if h < 0 {
		return 0
	}
	return h
}

// jwtExpiry extracts the "exp" claim from a JWT without verifying the signature.
func jwtExpiry(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	return claims.Exp
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func hoursUntil(unix int64) int {
	if unix <= 0 {
		return -1
	}
	return int(time.Until(time.Unix(unix, 0)).Hours())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
