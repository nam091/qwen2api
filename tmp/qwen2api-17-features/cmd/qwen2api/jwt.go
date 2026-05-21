package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// decodeJWTExp returns the exp claim of a JWT, or 0 if not parseable.
func decodeJWTExp(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try standard base64 with padding tolerance.
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return 0
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	return claims.Exp
}
