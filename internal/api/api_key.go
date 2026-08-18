package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const gatewayAPIKeyPrefix = "lt_"
const minGatewayAPIKeyLength = 16

// GenerateAPIKey returns a high-entropy key suitable for protecting the
// gateway's public HTTP listener.
func GenerateAPIKey() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return gatewayAPIKeyPrefix + base64.RawURLEncoding.EncodeToString(random), nil
}

// ValidateAPIKey rejects empty or trivially short configuration values before
// they can be used to protect a public listener.
func ValidateAPIKey(apiKey string) error {
	if len([]byte(strings.TrimSpace(apiKey))) < minGatewayAPIKeyLength {
		return fmt.Errorf("gateway API key must be at least %d bytes", minGatewayAPIKeyLength)
	}
	return nil
}

// APIKeyMiddleware requires either an OpenAI-compatible Bearer token or an
// Anthropic-compatible x-api-key header. OPTIONS is allowed through so CORS
// preflight requests never need to expose the secret.
func APIKeyMiddleware(apiKey string, next http.Handler) http.Handler {
	expected := strings.TrimSpace(apiKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if expected != "" && requestHasAPIKey(r, expected) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", `Bearer realm="lingma-tap"`)
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"message": "Invalid or missing API key",
				"type":    "authentication_error",
			},
		})
	})
}

func requestHasAPIKey(r *http.Request, expected string) bool {
	if constantTimeKeyEqual(strings.TrimSpace(r.Header.Get("x-api-key")), expected) {
		return true
	}

	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(authorization, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	return constantTimeKeyEqual(strings.TrimSpace(token), expected)
}

func constantTimeKeyEqual(provided, expected string) bool {
	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}
