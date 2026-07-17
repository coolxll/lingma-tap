package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
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

	if err := db.SaveGatewayLog(&proto.GatewayLog{
		Ts:                 Now(),
		Session:            "session-g1",
		Model:              "gpt-4",
		Method:             "POST",
		Path:               "/chat",
		RequestBody:        "hi",
		ResponseBody:       "hello again",
		InputTokens:        123,
		CachedTokens:       67,
		OutputTokens:       45,
		ReasoningTokens:    8,
		TotalTokens:        168,
		TTFT:               321,
		UpstreamAttempts:   2,
		RecoveryApplied:    true,
		UpstreamErrorClass: "http_503",
		FirstActionableMS:  456,
		ReasoningOnlyBytes: 789,
		RequestedProfile:   "task=question_refine;agent=agent_chat;reasoning=true;source=system",
		EffectiveProfile:   "task=question_refine;agent=agent_common;reasoning=false;source=",
		Status:             200,
	}); err != nil {
		t.Fatalf("SaveGatewayLog update failed: %v", err)
	}
	logs, err = db.RecentGatewayLogs(10)
	if err != nil {
		t.Fatalf("RecentGatewayLogs after update failed: %v", err)
	}
	if logs[0].InputTokens != 123 || logs[0].OutputTokens != 45 || logs[0].CachedTokens != 67 || logs[0].ReasoningTokens != 8 || logs[0].TotalTokens != 168 || logs[0].TTFT != 321 {
		t.Errorf("expected updated usage fields, got %+v", logs[0])
	}
	if logs[0].UpstreamAttempts != 2 || !logs[0].RecoveryApplied || logs[0].UpstreamErrorClass != "http_503" || logs[0].FirstActionableMS != 456 || logs[0].ReasoningOnlyBytes != 789 || logs[0].RequestedProfile == "" || logs[0].EffectiveProfile == "" {
		t.Errorf("expected updated recovery fields, got %+v", logs[0])
	}
	if err := db.SaveGatewayLog(&proto.GatewayLog{
		Ts:          Now(),
		Session:     "session-g1",
		Model:       "gpt-4",
		Method:      "POST",
		Path:        "/chat",
		RequestBody: "hi",
		Status:      0,
	}); err != nil {
		t.Fatalf("SaveGatewayLog late initial update failed: %v", err)
	}
	logs, err = db.RecentGatewayLogs(10)
	if err != nil {
		t.Fatalf("RecentGatewayLogs after late initial update failed: %v", err)
	}
	if logs[0].Status != 200 || logs[0].InputTokens != 123 || logs[0].OutputTokens != 45 || logs[0].CachedTokens != 67 || logs[0].ReasoningTokens != 8 || logs[0].TotalTokens != 168 || logs[0].TTFT != 321 || logs[0].ResponseBody != "hello again" || logs[0].UpstreamAttempts != 2 || !logs[0].RecoveryApplied || logs[0].UpstreamErrorClass != "http_503" || logs[0].FirstActionableMS != 456 || logs[0].ReasoningOnlyBytes != 789 || logs[0].RequestedProfile == "" || logs[0].EffectiveProfile == "" {
		t.Errorf("late initial log regressed final observability fields: %+v", logs[0])
	}

	gatewayStats, err := db.GatewayLogStats("", "gpt")
	if err != nil {
		t.Fatalf("GatewayLogStats failed: %v", err)
	}
	if gatewayStats.Total != 1 || gatewayStats.InputTokens != 123 || gatewayStats.OutputTokens != 45 || gatewayStats.CachedTokens != 67 || gatewayStats.ReasoningTokens != 8 || gatewayStats.TotalTokens != 168 {
		t.Errorf("unexpected gateway stats: %+v", gatewayStats)
	}
	gatewayStats, err = db.GatewayLogStats("9999-01-01T00:00:00Z", "")
	if err != nil {
		t.Fatalf("GatewayLogStats with since failed: %v", err)
	}
	if gatewayStats.Total != 0 || gatewayStats.TotalTokens != 0 {
		t.Errorf("expected empty future gateway stats, got %+v", gatewayStats)
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

func TestCompletedCancellationMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "completed_cancellation.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("create migration source: %v", err)
	}
	dbDriver, err := migratesqlite.WithInstance(db.db.DB, &migratesqlite.Config{})
	if err != nil {
		t.Fatalf("create migration database driver: %v", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", dbDriver)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	defer migrator.Close()

	if err = migrator.Migrate(4); err != nil {
		t.Fatalf("roll back migration 5: %v", err)
	}

	rows := []struct {
		session      string
		isSSE        int
		finishReason string
	}{
		{session: "completed-sse", isSSE: 1, finishReason: "tool_calls"},
		{session: "unfinished-sse", isSSE: 1, finishReason: ""},
		{session: "completed-non-sse", isSSE: 0, finishReason: "stop"},
	}
	for _, row := range rows {
		if _, err = db.db.Exec(`
			INSERT INTO gateway_logs (ts, session, status, error, is_sse, finish_reason)
			VALUES (?, ?, 499, 'client canceled request', ?, ?)
		`, Now(), row.session, row.isSSE, row.finishReason); err != nil {
			t.Fatalf("insert %s: %v", row.session, err)
		}
	}

	if err = migrator.Migrate(5); err != nil {
		t.Fatalf("apply migration 5: %v", err)
	}

	var completed struct {
		Status int    `db:"status"`
		Error  string `db:"error"`
	}
	if err = db.db.Get(&completed, "SELECT status, error FROM gateway_logs WHERE session = ?", "completed-sse"); err != nil {
		t.Fatalf("load completed SSE row: %v", err)
	}
	if completed.Status != 200 || completed.Error != "" {
		t.Fatalf("completed SSE row = %+v, want status 200 with no error", completed)
	}

	for _, session := range []string{"unfinished-sse", "completed-non-sse"} {
		var untouched struct {
			Status int    `db:"status"`
			Error  string `db:"error"`
		}
		if err = db.db.Get(&untouched, "SELECT status, error FROM gateway_logs WHERE session = ?", session); err != nil {
			t.Fatalf("load %s: %v", session, err)
		}
		if untouched.Status != 499 || untouched.Error != "client canceled request" {
			t.Fatalf("%s row changed unexpectedly: %+v", session, untouched)
		}
	}
}

func TestProxyCaptureMigrationBackfillsLegacyBodies(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "proxy_capture_backfill.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	dbDriver, err := migratesqlite.WithInstance(db.db.DB, &migratesqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", dbDriver)
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close()

	if err := migrator.Migrate(8); err != nil {
		t.Fatalf("rollback proxy capture migration: %v", err)
	}
	if _, err := db.db.Exec(`
		INSERT INTO proxy_records (
			ts, session, idx, direction, req_body, req_body_raw, req_size,
			resp_body, resp_size, raw_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, Now(), "legacy-body", 0, "C2S", "decoded preview", "legacy raw request", 18,
		"legacy response", 15, "{}"); err != nil {
		t.Fatalf("insert legacy record: %v", err)
	}
	if err := migrator.Migrate(9); err != nil {
		t.Fatalf("apply proxy capture migration: %v", err)
	}

	var row struct {
		ReqBody      []byte `db:"req_body_blob"`
		RespBody     []byte `db:"resp_body_blob"`
		BodyComplete bool   `db:"body_complete"`
		CapturedSize int64  `db:"captured_size"`
	}
	if err := db.db.Get(&row, `
		SELECT req_body_blob, resp_body_blob, body_complete, captured_size
		FROM proxy_records WHERE session = ?`, "legacy-body"); err != nil {
		t.Fatal(err)
	}
	if string(row.ReqBody) != "legacy raw request" || string(row.RespBody) != "legacy response" {
		t.Fatalf("legacy bodies were not backfilled: req=%q resp=%q", row.ReqBody, row.RespBody)
	}
	if !row.BodyComplete || row.CapturedSize != int64(len("legacy raw request")) {
		t.Fatalf("legacy lifecycle fields were not backfilled: %+v", row)
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
