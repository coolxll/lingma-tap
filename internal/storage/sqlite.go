package storage

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	db      *sqlx.DB
	writeMu sync.Mutex
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)

	d := &DB{db: sqlx.NewDb(db, "sqlite")}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	// 1. Migrate legacy 'records' table if it exists (legacy compatibility)
	var count int
	_ = d.db.Get(&count, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='records'")
	if count > 0 {
		log.Println("[sqlite] Migrating 'records' table to 'proxy_records'...")
		_, err := d.db.Exec("ALTER TABLE records RENAME TO proxy_records")
		if err != nil {
			log.Printf("[sqlite] Migration failed: %v", err)
		} else {
			// Also rename indexes for clarity
			d.db.Exec("DROP INDEX IF EXISTS idx_records_session")
			d.db.Exec("DROP INDEX IF EXISTS idx_records_ts")
		}
	}

	// 2. Run standard migrations using golang-migrate
	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	dbDriver, err := sqlite.WithInstance(d.db.DB, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("create migration db driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite", dbDriver)
	if err != nil {
		return fmt.Errorf("create migration instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}

// SaveRecord persists a record and upserts its session aggregate.
func (d *DB) SaveRecord(rec *proto.Record) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	// Prepare helper fields
	reqHeadersJSON, _ := json.Marshal(rec.ReqHeaders)
	respHeadersJSON, _ := json.Marshal(rec.RespHeaders)
	sseEventsJSON, _ := json.Marshal(rec.SSEEvents)

	rec.ReqHeadersJSON = string(reqHeadersJSON)
	rec.RespHeadersJSON = string(respHeadersJSON)
	rec.SSEEventsJSON = string(sseEventsJSON)
	rec.RawJSON = string(rec.ToJSON())

	tx, err := d.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert record
	_, err = tx.NamedExec(`
		INSERT INTO proxy_records (
			ts, session, idx, direction, method, url, host, path, is_encoded,
			endpoint_type, req_headers_json, req_body, req_body_raw, req_mime, req_size,
			status, status_text, resp_headers_json, resp_body, resp_mime, resp_size,
			is_sse, sse_events_json, error, source, raw_json
		) VALUES (
			:ts, :session, :idx, :direction, :method, :url, :host, :path, :is_encoded,
			:endpoint_type, :req_headers_json, :req_body, :req_body_raw, :req_mime, :req_size,
			:status, :status_text, :resp_headers_json, :resp_body, :resp_mime, :resp_size,
			:is_sse, :sse_events_json, :error, :source, :raw_json
		)
	`, rec)
	if err != nil {
		return err
	}

	// Upsert session
	_, err = tx.Exec(`
		INSERT INTO sessions (id, host, path, endpoint_type, record_count, first_ts, last_ts, req_size, resp_size, preview)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			record_count = record_count + 1,
			last_ts = excluded.last_ts,
			req_size = req_size + excluded.req_size,
			resp_size = resp_size + excluded.resp_size
	`,
		rec.Session, rec.Host, rec.Path, rec.EndpointType,
		rec.Ts, rec.Ts, rec.ReqSize, rec.RespSize, previewText(rec),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// RecentRecords returns the most recent records, optionally skipping the first `offset` records.
func (d *DB) RecentRecords(limit int, offset ...int) ([]proto.Record, error) {
	off := 0
	if len(offset) > 0 {
		off = offset[0]
	}

	var raws []string
	err := d.db.Select(&raws, "SELECT raw_json FROM proxy_records ORDER BY id DESC LIMIT ? OFFSET ?", limit, off)
	if err != nil {
		return nil, err
	}

	var records []proto.Record
	for _, raw := range raws {
		var rec proto.Record
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		records = append(records, rec)
	}

	return records, nil
}

// ClearTraffic deletes all records, sessions, and gateway logs.
func (d *DB) ClearTraffic() error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM proxy_records")
	tx.Exec("DELETE FROM sessions")
	tx.Exec("DELETE FROM gateway_logs")
	return tx.Commit()
}

// ClearTrafficBefore deletes records older than the specified date (RFC3339 format).
// It returns the total number of deleted records.
func (d *DB) ClearTrafficBefore(beforeDate string) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Delete old proxy records
	result, err := tx.Exec("DELETE FROM proxy_records WHERE ts < ?", beforeDate)
	if err != nil {
		return 0, err
	}
	proxyDeleted, _ := result.RowsAffected()

	// Delete orphan sessions (no remaining records)
	tx.Exec(`DELETE FROM sessions WHERE id NOT IN (SELECT DISTINCT session FROM proxy_records)`)

	// Delete old gateway logs
	result, err = tx.Exec("DELETE FROM gateway_logs WHERE ts < ?", beforeDate)
	if err != nil {
		return 0, err
	}
	gatewayDeleted, _ := result.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return int(proxyDeleted + gatewayDeleted), nil
}

// ListSessions returns sessions ordered by last_ts descending.
func (d *DB) ListSessions(limit int) ([]proto.Session, error) {
	var sessions []proto.Session
	err := d.db.Select(&sessions, `
		SELECT id, host, path, endpoint_type, record_count, first_ts, last_ts, req_size, resp_size, preview
		FROM sessions ORDER BY last_ts DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

func previewText(rec *proto.Record) string {
	if rec.ReqBody != "" {
		body := rec.ReqBody
		if len(body) > 200 {
			body = body[:200] + "..."
		}
		return body
	}
	return rec.EndpointType
}

// RecordCount returns the total number of records.
func (d *DB) RecordCount() int {
	var count int
	_ = d.db.Get(&count, "SELECT COUNT(*) FROM proxy_records")
	return count
}

// SessionCount returns the total number of sessions.
func (d *DB) SessionCount() int {
	var count int
	_ = d.db.Get(&count, "SELECT COUNT(*) FROM sessions")
	return count
}

// StorageStats holds summary statistics.
type StorageStats struct {
	Records  int    `json:"records"`
	Sessions int    `json:"sessions"`
	OldestTs string `json:"oldest_ts,omitempty"`
	NewestTs string `json:"newest_ts,omitempty"`
}

// Stats returns storage statistics.
func (d *DB) Stats() StorageStats {
	var s StorageStats
	_ = d.db.Get(&s.Records, "SELECT COUNT(*) FROM proxy_records")
	_ = d.db.Get(&s.Sessions, "SELECT COUNT(*) FROM sessions")
	_ = d.db.Get(&s.OldestTs, "SELECT MIN(ts) FROM proxy_records")
	_ = d.db.Get(&s.NewestTs, "SELECT MAX(ts) FROM proxy_records")
	return s
}

// CloseIdle is a no-op for compatibility.
func (d *DB) CloseIdle() {}

// Ping checks the database connection.
func (d *DB) Ping() error {
	return d.db.Ping()
}

// MustExec executes a query, ignoring errors. For migrations.
func (d *DB) MustExec(query string, args ...interface{}) {
	d.db.Exec(query, args...)
}

// SaveGatewayLog persists a gateway-specific log entry.
func (d *DB) SaveGatewayLog(log *proto.GatewayLog) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	sseEventsJSON, _ := json.Marshal(log.SSEEvents)
	log.SSEEventsJSON = string(sseEventsJSON)

	_, err := d.db.NamedExec(`
		INSERT INTO gateway_logs (ts, session, model, method, path, request_body, response_body,
			input_tokens, output_tokens, status, latency, error, is_sse, sse_events_json, finish_reason)
		VALUES (:ts, :session, :model, :method, :path, :request_body, :response_body,
			:input_tokens, :output_tokens, :status, :latency, :error, :is_sse, :sse_events_json, :finish_reason)
		ON CONFLICT(session) DO UPDATE SET
			response_body = excluded.response_body,
			output_tokens = excluded.output_tokens,
			status = excluded.status,
			latency = excluded.latency,
			error = excluded.error,
			is_sse = excluded.is_sse,
			sse_events_json = excluded.sse_events_json,
			finish_reason = excluded.finish_reason
	`, log)
	return err
}

// RecentGatewayLogs returns recent logs from the gateway_logs table, optionally skipping `offset` records.
func (d *DB) RecentGatewayLogs(limit int, offset ...int) ([]proto.GatewayLog, error) {
	off := 0
	if len(offset) > 0 {
		off = offset[0]
	}

	var logs []proto.GatewayLog
	err := d.db.Select(&logs, `
		SELECT id, ts, session, model, method, path, request_body, response_body,
			input_tokens, output_tokens, status, latency, error, is_sse, sse_events_json, finish_reason
		FROM gateway_logs ORDER BY id DESC LIMIT ? OFFSET ?
	`, limit, off)
	if err != nil {
		return nil, err
	}

	for i := range logs {
		json.Unmarshal([]byte(logs[i].SSEEventsJSON), &logs[i].SSEEvents)
	}

	return logs, nil
}

// GetSetting retrieves a setting value by key.
func (d *DB) GetSetting(key string) (string, error) {
	var value string
	err := d.db.Get(&value, "SELECT value FROM settings WHERE key = ?", key)
	if err != nil {
		return "", err
	}
	return value, nil
}

// SaveSetting persists a setting value.
func (d *DB) SaveSetting(key, value string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	_, err := d.db.Exec("INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// Now returns the current time in RFC3339Nano format.
func Now() string {
	return time.Now().Format(time.RFC3339Nano)
}
