package storage

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
	_ "modernc.org/sqlite"
)

type DB struct {
	db      *sql.DB
	writeMu sync.Mutex
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)

	d := &DB{db: db}
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
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS records (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			ts         TEXT NOT NULL,
			session    TEXT NOT NULL,
			idx        INTEGER NOT NULL,
			direction  TEXT NOT NULL,
			method     TEXT,
			url        TEXT,
			host       TEXT,
			path       TEXT,
			is_encoded INTEGER DEFAULT 0,
			endpoint_type TEXT,
			req_headers_json TEXT,
			req_body   TEXT,
			req_body_raw TEXT,
			req_mime   TEXT,
			req_size   INTEGER DEFAULT 0,
			status     INTEGER,
			status_text TEXT,
			resp_headers_json TEXT,
			resp_body  TEXT,
			resp_mime  TEXT,
			resp_size  INTEGER DEFAULT 0,
			is_sse     INTEGER DEFAULT 0,
			sse_events_json TEXT,
			error      TEXT,
			source     TEXT DEFAULT 'proxy',
			raw_json   TEXT NOT NULL,
			UNIQUE(session, idx)
		);
		CREATE INDEX IF NOT EXISTS idx_records_session ON records(session, idx);
		CREATE INDEX IF NOT EXISTS idx_records_ts ON records(ts);

		CREATE TABLE IF NOT EXISTS sessions (
			id           TEXT PRIMARY KEY,
			host         TEXT,
			path         TEXT,
			endpoint_type TEXT,
			record_count INTEGER DEFAULT 0,
			first_ts     TEXT,
			last_ts      TEXT,
			req_size     INTEGER DEFAULT 0,
			resp_size    INTEGER DEFAULT 0,
			preview      TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_sessions_last_ts ON sessions(last_ts);

		CREATE TABLE IF NOT EXISTS gateway_logs (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			ts            TEXT NOT NULL,
			session       TEXT NOT NULL,
			model         TEXT,
			method        TEXT,
			path          TEXT,
			request_body  TEXT,
			response_body TEXT,
			input_tokens  INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			status        INTEGER,
			latency       INTEGER, -- in milliseconds
			error         TEXT,
			is_sse        INTEGER DEFAULT 0,
			sse_events_json TEXT,
			UNIQUE(session)
		);
		CREATE INDEX IF NOT EXISTS idx_gateway_logs_ts ON gateway_logs(ts);
		CREATE INDEX IF NOT EXISTS idx_gateway_logs_model ON gateway_logs(model);
	`)
	return err
}

// SaveRecord persists a record and upserts its session aggregate.
func (d *DB) SaveRecord(rec *proto.Record) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rawJSON := rec.ToJSON()

	reqHeadersJSON, _ := json.Marshal(rec.ReqHeaders)
	respHeadersJSON, _ := json.Marshal(rec.RespHeaders)
	sseEventsJSON, _ := json.Marshal(rec.SSEEvents)

	_, err = tx.Exec(`
		INSERT INTO records (ts, session, idx, direction, method, url, host, path, is_encoded, endpoint_type,
			req_headers_json, req_body, req_body_raw, req_mime, req_size,
			status, status_text, resp_headers_json, resp_body, resp_mime, resp_size,
			is_sse, sse_events_json, error, source, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session, idx) DO UPDATE SET
			status = excluded.status, status_text = excluded.status_text,
			resp_headers_json = excluded.resp_headers_json, resp_body = excluded.resp_body,
			resp_mime = excluded.resp_mime, resp_size = excluded.resp_size,
			is_sse = excluded.is_sse, sse_events_json = excluded.sse_events_json,
			raw_json = excluded.raw_json
	`,
		rec.Ts, rec.Session, rec.Index, rec.Direction, rec.Method, rec.URL, rec.Host, rec.Path,
		boolToInt(rec.IsEncoded), rec.EndpointType,
		string(reqHeadersJSON), rec.ReqBody, rec.ReqBodyRaw, rec.ReqMime, rec.ReqSize,
		rec.Status, rec.StatusText, string(respHeadersJSON), rec.RespBody, rec.RespMime, rec.RespSize,
		boolToInt(rec.IsSSE), string(sseEventsJSON), rec.Error, rec.Source, string(rawJSON),
	)
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

// RecentRecords returns the most recent records.
func (d *DB) RecentRecords(limit int) ([]proto.Record, error) {
	rows, err := d.db.Query(`
		SELECT raw_json FROM records ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []proto.Record
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var rec proto.Record
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		records = append(records, rec)
	}

	// Reverse to chronological order
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records, nil
}

// ClearTraffic deletes all records and sessions.
func (d *DB) ClearTraffic() error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM records")
	tx.Exec("DELETE FROM sessions")
	return tx.Commit()
}

// ListSessions returns sessions ordered by last_ts descending.
func (d *DB) ListSessions(limit int) ([]proto.Session, error) {
	rows, err := d.db.Query(`
		SELECT id, host, path, endpoint_type, record_count, first_ts, last_ts, req_size, resp_size, preview
		FROM sessions ORDER BY last_ts DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []proto.Session
	for rows.Next() {
		var s proto.Session
		if err := rows.Scan(&s.ID, &s.Host, &s.Path, &s.EndpointType,
			&s.RecordCount, &s.FirstTs, &s.LastTs, &s.ReqSize, &s.RespSize, &s.Preview); err != nil {
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
	d.db.QueryRow("SELECT COUNT(*) FROM records").Scan(&count)
	return count
}

// SessionCount returns the total number of sessions.
func (d *DB) SessionCount() int {
	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
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
	d.db.QueryRow("SELECT COUNT(*) FROM records").Scan(&s.Records)
	d.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&s.Sessions)
	d.db.QueryRow("SELECT MIN(ts) FROM records").Scan(&s.OldestTs)
	d.db.QueryRow("SELECT MAX(ts) FROM records").Scan(&s.NewestTs)
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

	_, err := d.db.Exec(`
		INSERT INTO gateway_logs (ts, session, model, method, path, request_body, response_body,
			input_tokens, output_tokens, status, latency, error, is_sse, sse_events_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session) DO UPDATE SET
			response_body = excluded.response_body,
			output_tokens = excluded.output_tokens,
			status = excluded.status,
			latency = excluded.latency,
			error = excluded.error,
			is_sse = excluded.is_sse,
			sse_events_json = excluded.sse_events_json
	`,
		log.Ts, log.Session, log.Model, log.Method, log.Path, log.RequestBody, log.ResponseBody,
		log.InputTokens, log.OutputTokens, log.Status, log.Latency, log.Error,
		boolToInt(log.IsSSE), string(sseEventsJSON),
	)
	return err
}

// RecentGatewayLogs returns recent logs from the gateway_logs table.
func (d *DB) RecentGatewayLogs(limit int) ([]proto.GatewayLog, error) {
	rows, err := d.db.Query(`
		SELECT id, ts, session, model, method, path, request_body, response_body,
			input_tokens, output_tokens, status, latency, error, is_sse, sse_events_json
		FROM gateway_logs ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []proto.GatewayLog
	for rows.Next() {
		var l proto.GatewayLog
		var sseJSON string
		if err := rows.Scan(&l.ID, &l.Ts, &l.Session, &l.Model, &l.Method, &l.Path,
			&l.RequestBody, &l.ResponseBody, &l.InputTokens, &l.OutputTokens,
			&l.Status, &l.Latency, &l.Error, &l.IsSSE, &sseJSON); err != nil {
			continue
		}
		json.Unmarshal([]byte(sseJSON), &l.SSEEvents)
		logs = append(logs, l)
	}
	return logs, nil
}

// Now returns the current time in RFC3339Nano format.
func Now() string {
	return time.Now().Format(time.RFC3339Nano)
}
