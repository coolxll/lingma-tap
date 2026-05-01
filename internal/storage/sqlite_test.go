package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

func TestClearTraffic(t *testing.T) {
	// Create temp db
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer db.Close()
	defer os.RemoveAll(tmpDir)

	// Insert test data
	rec := &proto.Record{
		Ts:        time.Now().Format(time.RFC3339),
		Session:   "test-session",
		Index:     1,
		Direction:  "C2S",
		Method:     "POST",
		Path:       "/test",
		ReqBody:    "test request",
		RespBody:   "test response",
		Status:     200,
	}
	if err := db.SaveRecord(rec); err != nil {
		t.Fatalf("Failed to save record: %v", err)
	}

	// Verify record exists
	if db.RecordCount() != 1 {
		t.Errorf("Expected 1 record, got %d", db.RecordCount())
	}

	// Clear all
	if err := db.ClearTraffic(); err != nil {
		t.Fatalf("ClearTraffic failed: %v", err)
	}

	// Verify all tables cleared
	if db.RecordCount() != 0 {
		t.Errorf("Expected 0 records after clear, got %d", db.RecordCount())
	}
	if db.SessionCount() != 0 {
		t.Errorf("Expected 0 sessions after clear, got %d", db.SessionCount())
	}

	// Insert gateway log and verify it's also cleared
	gatewayLog := &proto.GatewayLog{
		Ts:        time.Now().Format(time.RFC3339),
		Session:   "gateway-session",
		Model:      "test-model",
		Method:     "POST",
		Path:       "/gateway",
		RequestBody: "req",
		ResponseBody: "resp",
		Status:     200,
	}
	if err := db.SaveGatewayLog(gatewayLog); err != nil {
		t.Fatalf("Failed to save gateway log: %v", err)
	}

	if err := db.ClearTraffic(); err != nil {
		t.Fatalf("ClearTraffic failed: %v", err)
	}

	// Verify gateway_logs also cleared (by checking RecentGatewayLogs returns empty)
	logs, err := db.RecentGatewayLogs(10)
	if err != nil {
		t.Fatalf("Failed to get gateway logs: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("Expected 0 gateway logs after clear, got %d", len(logs))
	}
}

func TestClearTrafficBefore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer db.Close()
	defer os.RemoveAll(tmpDir)

	// For simplicity, just test that ClearTrafficBefore doesn't crash
	cutoff := time.Now().AddDate(0, 0, -5).Format(time.RFC3339)
	deleted, err := db.ClearTrafficBefore(cutoff)
	if err != nil {
		t.Fatalf("ClearTrafficBefore failed: %v", err)
	}
	t.Logf("Deleted %d records", deleted)
}
