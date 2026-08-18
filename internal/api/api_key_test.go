package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateAPIKey(t *testing.T) {
	first, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	second, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if !strings.HasPrefix(first, gatewayAPIKeyPrefix) {
		t.Fatalf("key %q does not have expected prefix", first)
	}
	if len(first) < 40 {
		t.Fatalf("generated key is unexpectedly short: %d", len(first))
	}
	if first == second {
		t.Fatal("generated keys must be unique")
	}
	if err := ValidateAPIKey(first); err != nil {
		t.Fatalf("generated key failed validation: %v", err)
	}
}

func TestValidateAPIKey(t *testing.T) {
	if err := ValidateAPIKey("short"); err == nil {
		t.Fatal("expected short API key to be rejected")
	}
	if err := ValidateAPIKey("  lt_1234567890123  "); err != nil {
		t.Fatalf("expected valid API key, got %v", err)
	}
}

func TestAPIKeyMiddleware(t *testing.T) {
	const key = "lt_test-secret"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := APIKeyMiddleware(key, next)

	tests := []struct {
		name          string
		method        string
		authorization string
		xAPIKey       string
		wantStatus    int
	}{
		{name: "missing", method: http.MethodGet, wantStatus: http.StatusUnauthorized},
		{name: "wrong bearer", method: http.MethodGet, authorization: "Bearer wrong", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", method: http.MethodGet, authorization: "Basic " + key, wantStatus: http.StatusUnauthorized},
		{name: "bearer", method: http.MethodGet, authorization: "Bearer " + key, wantStatus: http.StatusNoContent},
		{name: "case insensitive scheme", method: http.MethodGet, authorization: "bearer " + key, wantStatus: http.StatusNoContent},
		{name: "anthropic header", method: http.MethodPost, xAPIKey: key, wantStatus: http.StatusNoContent},
		{name: "preflight", method: http.MethodOptions, wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/v1/models", nil)
			req.Header.Set("Authorization", tt.authorization)
			req.Header.Set("x-api-key", tt.xAPIKey)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("401 response is missing WWW-Authenticate")
			}
		})
	}
}

func TestAPIKeyMiddlewareFailsClosedWithEmptyKey(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	APIKeyMiddleware("", next).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}
