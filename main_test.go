package main

import (
	"path/filepath"
	"testing"

	"github.com/coolxll/lingma-tap/internal/api"
	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/coolxll/lingma-tap/internal/storage"
)

// newTestApp creates an App with real dependencies for testing.
func newTestApp(t *testing.T) (*App, *storage.DB, func()) {
	tmpDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	sink := storage.NewAsyncSink(db, 1000)
	hub := api.NewHub()
	go hub.Run()

	app := NewApp()
	app.db = db
	app.sink = sink
	app.hub = hub
	app.gatewayLogging = true
	app.proxyLogging = true

	cleanup := func() {
		sink.Close()
		db.Close()
	}

	return app, db, cleanup
}

func TestNewApp(t *testing.T) {
	app := NewApp()
	if app == nil {
		t.Fatal("expected non-nil App")
	}
	if !app.proxyLogging {
		t.Error("expected proxyLogging to be true by default")
	}
}

func TestGetRecords(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	// Insert some records
	app.db.SaveRecord(&proto.Record{Session: "s1", EndpointType: "chat", Method: "GET"})
	app.db.SaveRecord(&proto.Record{Session: "s2", EndpointType: "chat", Method: "POST"})

	result := app.GetRecords(10, 0)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 records, got %d", len(result))
	}
}

func TestGetRecordsByType(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	app.db.SaveRecord(&proto.Record{Session: "s1", EndpointType: "chat", Method: "GET"})
	app.db.SaveRecord(&proto.Record{Session: "s2", EndpointType: "embedding", Method: "POST"})

	result := app.GetRecordsByType(10, 0, "chat")
	if len(result) != 1 {
		t.Fatalf("expected 1 chat record, got %d", len(result))
	}
}

func TestGetGatewayLogs(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	// Insert a gateway log
	app.db.SaveGatewayLog(&proto.GatewayLog{Session: "l1", Model: "gpt-4"})

	result := app.GetGatewayLogs(10, 0)
	if len(result) < 1 {
		t.Fatalf("expected at least 1 log, got %d", len(result))
	}
}

func TestClearRecords(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	app.db.SaveRecord(&proto.Record{Session: "s1", EndpointType: "chat"})
	err := app.ClearRecords()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClearProxyRecords(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	err := app.ClearProxyRecords()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClearGatewayLogs(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	err := app.ClearGatewayLogs()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClearRecordsBefore(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	app.db.SaveRecord(&proto.Record{Session: "s1", EndpointType: "chat"})
	deleted, err := app.ClearRecordsBefore(100)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted record, got %d", deleted)
	}
}

func TestGetCACertPath(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	if path := app.GetCACertPath(); path != "" {
		t.Errorf("expected empty path when CA not initialized, got %q", path)
	}
}

func TestSetLogging(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	app.SetLogging(true)
	if !app.gatewayLogging {
		t.Error("expected gatewayLogging to be true")
	}

	val, _ := app.db.GetSetting("gateway_logging")
	if val != "true" {
		t.Errorf("expected gateway_logging='true' in db, got %q", val)
	}
}

func TestSetProxyLogging(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	app.SetProxyLogging(false)
	if app.proxyLogging {
		t.Error("expected proxyLogging to be false")
	}

	val, _ := app.db.GetSetting("proxy_logging")
	if val != "false" {
		t.Errorf("expected proxy_logging='false' in db, got %q", val)
	}
}

func TestGetStatus(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	status := app.GetStatus()
	if status == nil {
		t.Fatal("expected non-nil status")
	}

	if _, ok := status["ws_clients"]; !ok {
		t.Error("expected ws_clients in status")
	}
	if _, ok := status["stats"]; !ok {
		t.Error("expected stats in status")
	}
}

func TestGetModels_NoBridge(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	_, err := app.GetModels()
	if err == nil {
		t.Error("expected error when bridge is nil")
	}
}

func TestGetAnthropicMapping(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	// Set a mapping
	app.SaveAnthropicMapping(map[string]string{"sonnet": "dashscope_qwen3_coder"}, "dashscope_qmodel")

	mapping := app.GetAnthropicMapping()
	if mapping == nil {
		t.Fatal("expected non-nil mapping")
	}
	if m, ok := mapping["default_model"].(string); !ok || m != "dashscope_qmodel" {
		t.Errorf("expected default_model=dashscope_qmodel, got %v", mapping["default_model"])
	}
}

func TestSaveAnthropicMapping(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	mapping := map[string]string{"sonnet": "dashscope_qwen3_coder"}
	err := app.SaveAnthropicMapping(mapping, "dashscope_qmodel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it was saved
	mappingJSON, _ := app.db.GetSetting("anthropic_model_mapping")
	if mappingJSON == "" {
		t.Error("expected mapping to be saved in db")
	}
}

func TestGetVersion(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	version := app.GetVersion()
	if version == "" {
		t.Error("expected non-empty version")
	}
}

func TestRecentRecords(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	app.db.SaveRecord(&proto.Record{Session: "s1", EndpointType: "chat"})
	result, err := app.RecentRecords(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) < 1 {
		t.Errorf("expected at least 1 record, got %d", len(result))
	}
}

func TestClearTraffic(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	err := app.ClearTraffic()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStats(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	stats := app.Stats()
	if stats == nil {
		t.Error("expected non-nil stats")
	}
}

func TestApp_ImplementsRecordStore(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	// Verify App implements api.RecordStore interface
	var _ api.RecordStore = app

	// Test each method
	app.db.SaveRecord(&proto.Record{Session: "s1", EndpointType: "chat"})

	records, err := app.RecentRecords(10)
	if err != nil {
		t.Fatalf("RecentRecords error: %v", err)
	}
	if len(records) < 1 {
		t.Error("expected at least 1 record")
	}

	err = app.ClearTraffic()
	if err != nil {
		t.Fatalf("ClearTraffic error: %v", err)
	}

	s := app.Stats()
	if s == nil {
		t.Error("expected non-nil stats")
	}
}
