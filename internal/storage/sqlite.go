package storage

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	lingmaencoding "github.com/coolxll/lingma-tap/internal/encoding"
	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// preserveOnPartialUpdate returns a SQL fragment that keeps the existing
// column value when an upsert carries a partial (status=0) update, falling
// back to the incoming value otherwise. `guard` selects which existing
// column signals "already finalized" (the column itself for numeric fields,
// or `status` for text/payload fields that should follow the status lifecycle).
func preserveOnPartialUpdate(col, guard string) string {
	return col + " = CASE\n" +
		"\t\t\t\tWHEN excluded.status = 0 AND gateway_logs." + guard + " > 0 THEN gateway_logs." + col + "\n" +
		"\t\t\t\tELSE excluded." + col + "\n" +
		"\t\t\tEND"
}

func normalizeTimestamp(ts string) string {
	if ts == "" {
		return Now()
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return ts
	}
	if _, err := time.Parse(time.RFC3339, ts); err == nil {
		return ts
	}
	return Now()
}

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
	// Prepare helper fields
	rec.Ts = normalizeTimestamp(rec.Ts)
	reqHeadersJSON, _ := json.Marshal(rec.ReqHeaders)
	respHeadersJSON, _ := json.Marshal(rec.RespHeaders)
	sseEventsJSON, _ := json.Marshal(rec.SSEEvents)
	rec.CorrelationKeys = extractCorrelationKeys(rec)
	correlationKeysJSON, _ := json.Marshal(rec.CorrelationKeys)
	artifactIDsJSON, _ := json.Marshal(rec.ArtifactIDs)

	rec.ReqHeadersJSON = string(reqHeadersJSON)
	rec.RespHeadersJSON = string(respHeadersJSON)
	rec.SSEEventsJSON = string(sseEventsJSON)
	rec.CorrelationKeysJSON = string(correlationKeysJSON)
	rec.ArtifactIDsJSON = string(artifactIDsJSON)
	rec.RawJSON = string(rec.ToJSON())

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

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
			req_body_blob, status, status_text, resp_headers_json, resp_body, resp_body_blob,
			resp_mime, resp_size, is_sse, sse_events_json, error, source, raw_json,
			body_phase, body_complete, body_truncated, captured_size, declared_size,
			body_encoding, content_encoding, correlation_keys_json, artifact_ids_json
		) VALUES (
			:ts, :session, :idx, :direction, :method, :url, :host, :path, :is_encoded,
			:endpoint_type, :req_headers_json, :req_body, :req_body_raw, :req_mime, :req_size,
			:req_body_blob, :status, :status_text, :resp_headers_json, :resp_body, :resp_body_blob,
			:resp_mime, :resp_size, :is_sse, :sse_events_json, :error, :source, :raw_json,
			:body_phase, :body_complete, :body_truncated, :captured_size, :declared_size,
			:body_encoding, :content_encoding, :correlation_keys_json, :artifact_ids_json
		)
		ON CONFLICT(session, idx) DO UPDATE SET
			ts = excluded.ts,
			direction = excluded.direction,
			method = excluded.method,
			url = excluded.url,
			host = excluded.host,
			path = excluded.path,
			is_encoded = excluded.is_encoded,
			endpoint_type = excluded.endpoint_type,
			req_headers_json = excluded.req_headers_json,
			req_body = excluded.req_body,
			req_body_raw = excluded.req_body_raw,
			req_mime = excluded.req_mime,
			req_size = excluded.req_size,
			req_body_blob = excluded.req_body_blob,
			status = excluded.status,
			status_text = excluded.status_text,
			resp_headers_json = excluded.resp_headers_json,
			resp_body = excluded.resp_body,
			resp_body_blob = excluded.resp_body_blob,
			resp_mime = excluded.resp_mime,
			resp_size = excluded.resp_size,
			is_sse = excluded.is_sse,
			sse_events_json = excluded.sse_events_json,
			error = excluded.error,
			source = excluded.source,
			raw_json = excluded.raw_json,
			body_phase = excluded.body_phase,
			body_complete = excluded.body_complete,
			body_truncated = excluded.body_truncated,
			captured_size = excluded.captured_size,
			declared_size = excluded.declared_size,
			body_encoding = excluded.body_encoding,
			content_encoding = excluded.content_encoding,
			correlation_keys_json = excluded.correlation_keys_json,
			artifact_ids_json = excluded.artifact_ids_json
	`, rec)
	if err != nil {
		return err
	}
	if err := tx.Get(&rec.ID, "SELECT id FROM proxy_records WHERE session = ? AND idx = ?", rec.Session, rec.Index); err != nil {
		return err
	}

	// Replace deterministic links for this lifecycle snapshot. Artifact rows
	// are only replaced when a complete multipart body was captured.
	if _, err := tx.Exec("DELETE FROM record_correlation_keys WHERE record_id = ?", rec.ID); err != nil {
		return err
	}
	for _, key := range rec.CorrelationKeys {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO record_correlation_keys(record_id, key_type, key_value) VALUES (?, ?, ?)`, rec.ID, parts[0], parts[1]); err != nil {
			return err
		}
	}
	artifacts := parseImageArtifacts(rec)
	if len(artifacts) > 0 {
		if _, err := tx.Exec("DELETE FROM record_artifacts WHERE record_id = ?", rec.ID); err != nil {
			return err
		}
		rec.ArtifactIDs = nil
		for _, artifact := range artifacts {
			result, err := tx.Exec(`
				INSERT INTO record_artifacts(record_id, field_name, filename, mime, size, sha256, body, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				rec.ID, artifact.Field, artifact.Filename, artifact.MIME, len(artifact.Body), artifactSHA256(artifact.Body), artifact.Body, rec.Ts)
			if err != nil {
				return err
			}
			artifactID, err := result.LastInsertId()
			if err != nil {
				return err
			}
			rec.ArtifactIDs = append(rec.ArtifactIDs, artifactID)
		}
	}
	artifactIDsJSON, _ = json.Marshal(rec.ArtifactIDs)
	rec.ArtifactIDsJSON = string(artifactIDsJSON)
	rec.RawJSON = string(rec.ToJSON())
	if _, err := tx.Exec(`UPDATE proxy_records SET raw_json = ?, artifact_ids_json = ? WHERE id = ?`, rec.RawJSON, rec.ArtifactIDsJSON, rec.ID); err != nil {
		return err
	}

	// Upsert session
	_, err = tx.Exec(`
		INSERT INTO sessions (id, host, path, endpoint_type, record_count, first_ts, last_ts, req_size, resp_size, preview)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			record_count = (SELECT COUNT(*) FROM proxy_records WHERE session = excluded.id),
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

	return recordsFromRawJSON(raws), nil
}

// RecentRecordsByType returns recent records filtered by endpoint type.
func (d *DB) RecentRecordsByType(limit int, offset int, recordType string) ([]proto.Record, error) {
	if recordType == "" || recordType == "all" {
		return d.RecentRecords(limit, offset)
	}

	var raws []string
	var err error
	if recordType == "other" {
		err = d.db.Select(
			&raws,
			"SELECT raw_json FROM proxy_records WHERE endpoint_type IN ('other', 'tracking', 'finish') ORDER BY id DESC LIMIT ? OFFSET ?",
			limit,
			offset,
		)
	} else {
		err = d.db.Select(
			&raws,
			"SELECT raw_json FROM proxy_records WHERE endpoint_type = ? ORDER BY id DESC LIMIT ? OFFSET ?",
			recordType,
			limit,
			offset,
		)
	}
	if err != nil {
		return nil, err
	}

	return recordsFromRawJSON(raws), nil
}

// GetRecordBody returns the captured raw body for one record. It deliberately
// bypasses raw_json so binary payloads are returned byte-for-byte.
func (d *DB) GetRecordBody(id int64) (body []byte, mime string, truncated bool, err error) {
	var row struct {
		Direction string `db:"direction"`
		ReqMime   string `db:"req_mime"`
		RespMime  string `db:"resp_mime"`
		ReqBody   []byte `db:"req_body_blob"`
		RespBody  []byte `db:"resp_body_blob"`
		Truncated bool   `db:"body_truncated"`
	}
	if err = d.db.Get(&row, `
		SELECT direction, req_mime, resp_mime, req_body_blob, resp_body_blob, body_truncated
		FROM proxy_records WHERE id = ?`, id); err != nil {
		return nil, "", false, err
	}
	return recordBodyFromRow(row.Direction, row.ReqMime, row.RespMime, row.ReqBody, row.RespBody, row.Truncated)
}

func (d *DB) GetRecordBodyByKey(session string, index int) (body []byte, mime string, truncated bool, err error) {
	var row struct {
		Direction string `db:"direction"`
		ReqMime   string `db:"req_mime"`
		RespMime  string `db:"resp_mime"`
		ReqBody   []byte `db:"req_body_blob"`
		RespBody  []byte `db:"resp_body_blob"`
		Truncated bool   `db:"body_truncated"`
	}
	if err = d.db.Get(&row, `
		SELECT direction, req_mime, resp_mime, req_body_blob, resp_body_blob, body_truncated
		FROM proxy_records WHERE session = ? AND idx = ?`, session, index); err != nil {
		return nil, "", false, err
	}
	return recordBodyFromRow(row.Direction, row.ReqMime, row.RespMime, row.ReqBody, row.RespBody, row.Truncated)
}

func (d *DB) GetRecordBodyDecoded(id int64) ([]byte, string, bool, error) {
	body, mime, truncated, err := d.GetRecordBody(id)
	if err != nil || truncated {
		return body, mime, truncated, err
	}
	var meta struct {
		Direction string `db:"direction"`
		Encoded   bool   `db:"is_encoded"`
	}
	if err := d.db.Get(&meta, "SELECT direction, is_encoded FROM proxy_records WHERE id = ?", id); err != nil || meta.Direction != "C2S" || !meta.Encoded {
		return body, mime, truncated, err
	}
	decoded, err := lingmaencoding.Decode(string(body))
	if err != nil {
		return body, mime, truncated, nil
	}
	return decoded, mime, false, nil
}

func (d *DB) GetRecordBodyDecodedByKey(session string, index int) ([]byte, string, bool, error) {
	body, mime, truncated, err := d.GetRecordBodyByKey(session, index)
	if err != nil || truncated {
		return body, mime, truncated, err
	}
	var meta struct {
		Direction string `db:"direction"`
		Encoded   bool   `db:"is_encoded"`
	}
	if err := d.db.Get(&meta, "SELECT direction, is_encoded FROM proxy_records WHERE session = ? AND idx = ?", session, index); err != nil || meta.Direction != "C2S" || !meta.Encoded {
		return body, mime, truncated, err
	}
	decoded, err := lingmaencoding.Decode(string(body))
	if err != nil {
		return body, mime, truncated, nil
	}
	return decoded, mime, false, nil
}

func recordBodyFromRow(direction, reqMime, respMime string, reqBody, respBody []byte, truncated bool) ([]byte, string, bool, error) {
	if direction == "C2S" {
		return reqBody, reqMime, truncated, nil
	}
	return respBody, respMime, truncated, nil
}

func (d *DB) GetArtifacts(recordID int64) ([]proto.Artifact, error) {
	var artifacts []proto.Artifact
	err := d.db.Select(&artifacts, `
		SELECT id, record_id, field_name, filename, mime, size, sha256
		FROM record_artifacts WHERE record_id = ? ORDER BY id`, recordID)
	return artifacts, err
}

func (d *DB) GetArtifactBody(id int64) ([]byte, string, error) {
	var row struct {
		Body []byte `db:"body"`
		MIME string `db:"mime"`
	}
	err := d.db.Get(&row, `SELECT body, mime FROM record_artifacts WHERE id = ?`, id)
	return row.Body, row.MIME, err
}

func recordsFromRawJSON(raws []string) []proto.Record {
	var records []proto.Record
	for _, raw := range raws {
		var rec proto.Record
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		records = append(records, rec)
	}

	return records
}

// ClearTraffic deletes all records, sessions, gateway logs, and responses state.
func (d *DB) ClearTraffic() error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM proxy_records"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM sessions"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM gateway_logs"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM responses_state"); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearProxyRecords deletes all proxy records and sessions.
func (d *DB) ClearProxyRecords() error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM proxy_records"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM sessions"); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearGatewayLogs deletes all gateway logs.
func (d *DB) ClearGatewayLogs() error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	_, err := d.db.Exec("DELETE FROM gateway_logs")
	return err
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
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id NOT IN (SELECT DISTINCT session FROM proxy_records)`); err != nil {
		return 0, err
	}

	// Delete old gateway logs
	result, err = tx.Exec("DELETE FROM gateway_logs WHERE ts < ?", beforeDate)
	if err != nil {
		return 0, err
	}
	gatewayDeleted, _ := result.RowsAffected()

	// Delete old responses state
	result, err = tx.Exec("DELETE FROM responses_state WHERE created_at < ?", beforeDate)
	if err != nil {
		return 0, err
	}
	responsesDeleted, _ := result.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return int(proxyDeleted + gatewayDeleted + responsesDeleted), nil
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
	log.Ts = normalizeTimestamp(log.Ts)
	sseEventsJSON, _ := json.Marshal(log.SSEEvents)
	log.SSEEventsJSON = string(sseEventsJSON)

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	_, err := d.db.NamedExec(`
		INSERT INTO gateway_logs (ts, session, model, method, path, request_body, response_body,
			input_tokens, output_tokens, cached_tokens, reasoning_tokens, total_tokens, ttft,
			upstream_attempts, recovery_applied, upstream_error_class, first_actionable_ms,
			reasoning_only_bytes, requested_profile, effective_profile,
			context_trimmed, context_original_bytes, context_trimmed_bytes,
			responses_degraded, responses_warnings,
			status, latency, error, is_sse, sse_events_json, finish_reason)
		VALUES (:ts, :session, :model, :method, :path, :request_body, :response_body,
			:input_tokens, :output_tokens, :cached_tokens, :reasoning_tokens, :total_tokens, :ttft,
			:upstream_attempts, :recovery_applied, :upstream_error_class, :first_actionable_ms,
			:reasoning_only_bytes, :requested_profile, :effective_profile,
			:context_trimmed, :context_original_bytes, :context_trimmed_bytes,
			:responses_degraded, :responses_warnings,
			:status, :latency, :error, :is_sse, :sse_events_json, :finish_reason)
		ON CONFLICT(session) DO UPDATE SET
			model = excluded.model,
			method = excluded.method,
			path = excluded.path,
			request_body = excluded.request_body,
			`+preserveOnPartialUpdate("response_body", "status")+`,
			`+preserveOnPartialUpdate("input_tokens", "input_tokens")+`,
			`+preserveOnPartialUpdate("output_tokens", "output_tokens")+`,
			`+preserveOnPartialUpdate("cached_tokens", "cached_tokens")+`,
			`+preserveOnPartialUpdate("reasoning_tokens", "reasoning_tokens")+`,
			`+preserveOnPartialUpdate("total_tokens", "total_tokens")+`,
			`+preserveOnPartialUpdate("ttft", "ttft")+`,
			`+preserveOnPartialUpdate("upstream_attempts", "status")+`,
			`+preserveOnPartialUpdate("recovery_applied", "status")+`,
			`+preserveOnPartialUpdate("upstream_error_class", "status")+`,
			`+preserveOnPartialUpdate("first_actionable_ms", "status")+`,
			`+preserveOnPartialUpdate("reasoning_only_bytes", "status")+`,
			`+preserveOnPartialUpdate("requested_profile", "status")+`,
			`+preserveOnPartialUpdate("effective_profile", "status")+`,
			context_trimmed = excluded.context_trimmed,
			context_original_bytes = excluded.context_original_bytes,
			context_trimmed_bytes = excluded.context_trimmed_bytes,
			responses_degraded = excluded.responses_degraded,
			responses_warnings = excluded.responses_warnings,
			`+preserveOnPartialUpdate("status", "status")+`,
			`+preserveOnPartialUpdate("latency", "latency")+`,
			`+preserveOnPartialUpdate("error", "status")+`,
			is_sse = excluded.is_sse,
			`+preserveOnPartialUpdate("sse_events_json", "status")+`,
			`+preserveOnPartialUpdate("finish_reason", "status")+`
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
			input_tokens, output_tokens, cached_tokens, reasoning_tokens, total_tokens, ttft,
			upstream_attempts, recovery_applied, upstream_error_class, first_actionable_ms,
			reasoning_only_bytes, requested_profile, effective_profile,
			context_trimmed, context_original_bytes, context_trimmed_bytes,
			responses_degraded, responses_warnings,
			status, latency, error, is_sse, sse_events_json, finish_reason
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

// GatewayLogStats returns aggregate statistics for gateway logs, optionally
// scoped by a lower-bound timestamp and the same search fields used by the UI.
func (d *DB) GatewayLogStats(sinceTs string, filter string) (proto.GatewayLogStats, error) {
	query := `
		SELECT
			COUNT(*) AS total,
			COALESCE(SUM(input_tokens), 0) AS input_tokens,
			COALESCE(SUM(output_tokens), 0) AS output_tokens,
			COALESCE(SUM(cached_tokens), 0) AS cached_tokens,
			COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens,
			COALESCE(SUM(CASE WHEN total_tokens > 0 THEN total_tokens ELSE input_tokens + output_tokens END), 0) AS total_tokens
		FROM gateway_logs
		WHERE 1=1`
	args := []any{}
	if sinceTs != "" {
		query += " AND ts >= ?"
		args = append(args, sinceTs)
	}
	if filter != "" {
		like := "%" + filter + "%"
		query += ` AND (
			LOWER(COALESCE(model, '')) LIKE LOWER(?) OR
			LOWER(COALESCE(path, '')) LIKE LOWER(?) OR
			LOWER(COALESCE(session, '')) LIKE LOWER(?) OR
			LOWER(COALESCE(request_body, '')) LIKE LOWER(?)
		)`
		args = append(args, like, like, like, like)
	}

	var stats proto.GatewayLogStats
	if err := d.db.Get(&stats, query, args...); err != nil {
		return proto.GatewayLogStats{}, err
	}
	return stats, nil
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
