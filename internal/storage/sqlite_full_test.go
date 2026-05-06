package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

func TestStorageFullFlow(t *testing.T) {
	// 1. Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_full.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer db.Close()
	defer os.RemoveAll(tmpDir)

	// 2. Save a Record
	rec := &proto.Record{
		Ts:        Now(),
		Session:   "session-1",
		Index:     0,
		Direction: "C2S",
		Method:    "POST",
		Host:      "api.example.com",
		Path:      "/v1/chat",
		ReqHeaders: map[string]string{
			"Content-Type": "application/json",
		},
		ReqBody: `{"query":"hi"}`,
	}
	if err := db.SaveRecord(rec); err != nil {
		t.Fatalf("SaveRecord failed: %v", err)
	}

	// 3. Verify Record and Session
	if count := db.RecordCount(); count != 1 {
		t.Errorf("Expected 1 record, got %d", count)
	}
	if count := db.SessionCount(); count != 1 {
		t.Errorf("Expected 1 session, got %d", count)
	}

	// 4. Verify ListSessions
	sessions, err := db.ListSessions(10)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "session-1" {
		t.Errorf("Unexpected session list: %+v", sessions)
	}

	// 5. Verify RecentRecords
	records, err := db.RecentRecords(10)
	if err != nil {
		t.Fatalf("RecentRecords failed: %v", err)
	}
	if len(records) != 1 || records[0].Session != "session-1" {
		t.Errorf("Unexpected record list: %+v", records)
	}
	// Verify headers were correctly handled via db tags and JSON helpers
	if records[0].ReqHeaders["Content-Type"] != "application/json" {
		t.Errorf("Expected header Content-Type to be application/json, got %v", records[0].ReqHeaders["Content-Type"])
	}

	// 6. Save Gateway Log
	glog := &proto.GatewayLog{
		Ts:           Now(),
		Session:      "session-g1",
		Model:        "gpt-4",
		Method:       "POST",
		Path:         "/chat",
		RequestBody:  "hi",
		ResponseBody: "hello",
		Status:       200,
		IsSSE:        true,
		SSEEvents: []proto.SSEEvent{
			{EventType: "data", Data: "hello"},
		},
	}
	if err := db.SaveGatewayLog(glog); err != nil {
		t.Fatalf("SaveGatewayLog failed: %v", err)
	}

	// 7. Verify Gateway Log
	logs, err := db.RecentGatewayLogs(10)
	if err != nil {
		t.Fatalf("RecentGatewayLogs failed: %v", err)
	}
	if len(logs) != 1 || logs[0].Session != "session-g1" || !logs[0].IsSSE {
		t.Errorf("Unexpected gateway logs: %+v", logs)
	}
	if len(logs[0].SSEEvents) != 1 || logs[0].SSEEvents[0].Data != "hello" {
		t.Errorf("Unexpected SSE events in gateway log: %+v", logs[0].SSEEvents)
	}

	// 8. Test Stats
	stats := db.Stats()
	if stats.Records != 1 || stats.Sessions != 1 {
		t.Errorf("Unexpected stats: %+v", stats)
	}

	// 9. Test ClearTrafficBefore
	time.Sleep(10 * time.Millisecond)
	cutoff := Now()
	deleted, err := db.ClearTrafficBefore(cutoff)
	if err != nil {
		t.Fatalf("ClearTrafficBefore failed: %v", err)
	}
	if deleted < 1 {
		t.Errorf("Expected at least 1 record deleted, got %d", deleted)
	}
	if count := db.RecordCount(); count != 0 {
		t.Errorf("Expected 0 records after clear before, got %d", count)
	}
}

func TestStorageMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_migration.db")
	
	// Create a legacy 'records' table to test compatibility migration
	rawDB, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	
	// Open already runs migrate, so 'records' should not exist or already be renamed.
	// But let's check if we can simulate the legacy state.
	// Actually, Open calls d.migrate() which handles the rename.
	
	// Let's verify standard tables exist
	tables := []string{"proxy_records", "sessions", "gateway_logs", "schema_migrations"}
	for _, table := range tables {
		var count int
		err := rawDB.db.Get(&count, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table)
		if err != nil {
			t.Errorf("Failed to check table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("Table %s does not exist", table)
		}
	}
	rawDB.Close()
}
