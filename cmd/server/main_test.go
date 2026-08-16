package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/bridge"
	"github.com/coolxll/lingma-tap/internal/storage"
)

func newTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	s := NewServer(tmpDir, db, false)
	cleanup := func() {
		db.Close()
	}
	return s, cleanup
}

func TestNewServer(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	if s.DataDir == "" {
		t.Error("expected DataDir to be set")
	}
	if s.DB == nil {
		t.Error("expected DB to be set")
	}
	if s.Handler == nil {
		t.Error("expected Handler to be set")
	}
}

func TestHandleHealth(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	s.HandleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", result)
	}
}

func TestHandleAuthStatus_Unauthenticated(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	w := httptest.NewRecorder()
	s.HandleAuthStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["authenticated"] != false {
		t.Errorf("expected authenticated=false, got %v", result)
	}
}

func TestHandleAuthStatus_Authenticated(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	// Set a mock bridge
	s.BridgeInst = &bridge.BridgeHandler{}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	w := httptest.NewRecorder()
	s.HandleAuthStatus(w, req)

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["authenticated"] != true {
		t.Errorf("expected authenticated=true, got %v", result)
	}
}

func TestHandleServerStatus(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	s.HandleServerStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := result["authenticated"]; !ok {
		t.Error("expected authenticated in response")
	}
	if _, ok := result["stats"]; !ok {
		t.Error("expected stats in response")
	}
}

func TestHandleGatewayLogs(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/logs", nil)
	w := httptest.NewRecorder()
	s.HandleGatewayLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result []interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 logs, got %d", len(result))
	}
}

func TestHandleAuthUpload_Success(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	// Mock auth functions
	s.LoadCredentialsFromBytes = func(id, user string) (*auth.Credentials, error) {
		return &auth.Credentials{Name: "Test User", UID: "test-uid"}, nil
	}
	s.SaveCredentialsToDir = func(dataDir, id, user string) error {
		return nil
	}

	// Build multipart form
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	idPart, _ := writer.CreateFormFile("id", "id")
	io.WriteString(idPart, "test-id-data")
	userPart, _ := writer.CreateFormFile("user", "user")
	io.WriteString(userPart, "test-user-data")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	s.HandleAuthUpload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["ok"] != true {
		t.Errorf("expected ok=true, got %v", result)
	}
	if result["user"] != "Test User" {
		t.Errorf("expected user='Test User', got %v", result["user"])
	}
}

func TestHandleAuthUpload_InvalidMethod(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/auth/upload", nil)
	w := httptest.NewRecorder()
	s.HandleAuthUpload(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleAuthUpload_InvalidCredentials(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	s.LoadCredentialsFromBytes = func(id, user string) (*auth.Credentials, error) {
		return nil, fmt.Errorf("invalid credentials")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	idPart, _ := writer.CreateFormFile("id", "id")
	io.WriteString(idPart, "test-id-data")
	userPart, _ := writer.CreateFormFile("user", "user")
	io.WriteString(userPart, "test-user-data")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	s.HandleAuthUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRegisterManagementRoutes(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	mux := http.NewServeMux()
	s.RegisterManagementRoutes(mux)

	paths := []string{
		"/api/health",
		"/api/auth/status",
		"/api/auth/upload",
		"/api/status",
		"/api/gateway/logs",
	}

	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		// Routes should be registered (not 404)
		if w.Code == http.StatusNotFound {
			t.Errorf("route %s not registered", path)
		}
	}
	for _, path := range []string{"/api/auth/probe", "/api/auth/callback"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("legacy OAuth route %s returned %d, want 404", path, w.Code)
		}
	}
}
