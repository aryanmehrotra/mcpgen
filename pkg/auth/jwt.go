package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// tokenExpiry decodes a JWT access token (no signature check) and returns the
// `exp` claim as time.Time. Returns an error if the token is malformed or
// missing the claim; the caller decides how to fall back.
func tokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("jwt: expected at least 2 segments, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("jwt: base64 decode payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("jwt: unmarshal claims: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("jwt: no exp claim")
	}
	return time.Unix(claims.Exp, 0), nil
}
