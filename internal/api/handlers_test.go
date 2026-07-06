package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coolxll/lingma-tap/internal/bridge"
	"github.com/coolxll/lingma-tap/internal/proto"
)

// mockRecordStore implements RecordStore for testing.
type mockRecordStore struct {
	records       []proto.Record
	clearErr      error
	recentRecords func(int) ([]proto.Record, error)
	stats         interface{}
}

func (m *mockRecordStore) RecentRecords(limit int) ([]proto.Record, error) {
	if m.recentRecords != nil {
		return m.recentRecords(limit)
	}
	return m.records, nil
}

func (m *mockRecordStore) ClearTraffic() error {
	return m.clearErr
}

func (m *mockRecordStore) Stats() interface{} {
	if m.stats != nil {
		return m.stats
	}
	return map[string]interface{}{}
}

// mockBridgeHandler implements BridgeHandler for testing.
type mockBridgeHandler struct {
	models      []bridge.ModelInfo
	modelsErr   error
	handleCalls map[string]int
}

func newMockBridgeHandler() *mockBridgeHandler {
	return &mockBridgeHandler{handleCalls: make(map[string]int)}
}

func (m *mockBridgeHandler) HandleModels(w http.ResponseWriter, r *http.Request) {
	m.handleCalls["HandleModels"]++
	w.WriteHeader(http.StatusOK)
}

func (m *mockBridgeHandler) HandleOpenAIChat(w http.ResponseWriter, r *http.Request) {
	m.handleCalls["HandleOpenAIChat"]++
	w.WriteHeader(http.StatusOK)
}

func (m *mockBridgeHandler) HandleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	m.handleCalls["HandleOpenAIResponses"]++
	w.WriteHeader(http.StatusOK)
}

func (m *mockBridgeHandler) HandleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	m.handleCalls["HandleAnthropicMessages"]++
	w.WriteHeader(http.StatusOK)
}

func (m *mockBridgeHandler) GetModels() ([]bridge.ModelInfo, error) {
	if m.modelsErr != nil {
		return nil, m.modelsErr
	}
	return m.models, nil
}

func TestNewHandler(t *testing.T) {
	hub := NewHub()
	store := &mockRecordStore{}
	bh := newMockBridgeHandler()

	h := NewHandler(hub, store, bh)
	if h == nil {
		t.Fatal("expected non-nil Handler")
	}
	if h.hub != hub || h.store != store || h.bridge != bh {
		t.Error("handler fields not set correctly")
	}
}

func TestNewGatewayHandler(t *testing.T) {
	bh := newMockBridgeHandler()
	h := NewGatewayHandler(bh)
	if h == nil {
		t.Fatal("expected non-nil Handler")
	}
	if h.hub != nil || h.store != nil {
		t.Error("gateway handler should have nil hub and store")
	}
	if h.bridge != bh {
		t.Error("gateway handler bridge not set")
	}
}

func TestSetBridge(t *testing.T) {
	h := NewHandler(NewHub(), &mockRecordStore{}, nil)
	bh := newMockBridgeHandler()
	h.SetBridge(bh)
	if h.bridge != bh {
		t.Error("SetBridge did not update bridge")
	}
}

func TestIsBridgeNil(t *testing.T) {
	tests := []struct {
		name   string
		bridge BridgeHandler
		want   bool
	}{
		{"nil", nil, true},
		{"non-nil mock", newMockBridgeHandler(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(NewHub(), &mockRecordStore{}, tt.bridge)
			if got := h.isBridgeNil(); got != tt.want {
				t.Errorf("isBridgeNil() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleRecords_GET(t *testing.T) {
	store := &mockRecordStore{
		records: []proto.Record{{ID: 1, Session: "s1"}, {ID: 2, Session: "s2"}},
	}
	h := NewHandler(NewHub(), store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/records", nil)
	w := httptest.NewRecorder()
	h.handleRecords(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result []proto.Record
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 records, got %d", len(result))
	}
}

func TestHandleRecords_GETWithLimit(t *testing.T) {
	store := &mockRecordStore{
		recentRecords: func(limit int) ([]proto.Record, error) {
			return []proto.Record{{ID: int64(limit)}}, nil
		},
	}
	h := NewHandler(NewHub(), store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/records?limit=50", nil)
	w := httptest.NewRecorder()
	h.handleRecords(w, req)

	var result []proto.Record
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result) != 1 || result[0].ID != 50 {
		t.Fatalf("expected limit=50 to be passed, got %+v", result)
	}
}

func TestHandleRecords_GETStoreError(t *testing.T) {
	store := &mockRecordStore{
		recentRecords: func(limit int) ([]proto.Record, error) {
			return nil, errors.New("db error")
		},
	}
	h := NewHandler(NewHub(), store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/records", nil)
	w := httptest.NewRecorder()
	h.handleRecords(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHandleRecords_DELETE(t *testing.T) {
	store := &mockRecordStore{}
	h := NewHandler(NewHub(), store, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/records", nil)
	w := httptest.NewRecorder()
	h.handleRecords(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result["ok"] {
		t.Error("expected ok=true")
	}
}

func TestHandleRecords_DELETEError(t *testing.T) {
	store := &mockRecordStore{clearErr: errors.New("clear failed")}
	h := NewHandler(NewHub(), store, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/records", nil)
	w := httptest.NewRecorder()
	h.handleRecords(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHandleRecords_OPTIONS(t *testing.T) {
	h := NewHandler(NewHub(), &mockRecordStore{}, nil)

	req := httptest.NewRequest(http.MethodOptions, "/api/records", nil)
	w := httptest.NewRecorder()
	h.handleRecords(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("expected Access-Control-Allow-Methods header")
	}
}

func TestHandleRecords_UnsupportedMethod(t *testing.T) {
	h := NewHandler(NewHub(), &mockRecordStore{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/records", nil)
	w := httptest.NewRecorder()
	h.handleRecords(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleStatus(t *testing.T) {
	store := &mockRecordStore{stats: map[string]interface{}{"records": 42}}
	h := NewHandler(NewHub(), store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	h.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := result["ws_clients"]; !ok {
		t.Error("expected ws_clients in response")
	}
	if _, ok := result["stats"]; !ok {
		t.Error("expected stats in response")
	}
}

func TestRegisterGatewayRoutes_WithBridge(t *testing.T) {
	bh := newMockBridgeHandler()
	h := NewHandler(NewHub(), &mockRecordStore{}, bh)
	mux := http.NewServeMux()
	h.RegisterGatewayRoutes(mux)

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/models"},
		{http.MethodGet, "/v1/models/"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/v1/responses"},
		{http.MethodPost, "/v1/messages"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			// The mock returns 200; if route missing the mux returns 404.
			if w.Code == http.StatusNotFound {
				t.Fatalf("route %s not registered", tt.path)
			}
		})
	}
}

func TestRegisterGatewayRoutes_WithoutBridge(t *testing.T) {
	h := NewHandler(NewHub(), &mockRecordStore{}, nil)
	mux := http.NewServeMux()
	h.RegisterGatewayRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when bridge is nil, got %d", w.Code)
	}
}

func TestCorsMiddleware_OPTIONS(t *testing.T) {
	called := false
	handler := corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if called {
		t.Error("expected handler not to be called for OPTIONS")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected CORS origin *, got %q", got)
	}
}

func TestCorsMiddleware_NonOPTIONS(t *testing.T) {
	called := false
	handler := corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if !called {
		t.Error("expected handler to be called for non-OPTIONS")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected CORS origin *, got %q", got)
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]string{"key": "value"})

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var result map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["key"] != "value" {
		t.Errorf("unexpected result: %v", result)
	}
}
