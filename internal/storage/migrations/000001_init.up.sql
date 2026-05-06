CREATE TABLE IF NOT EXISTS proxy_records (
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
CREATE INDEX IF NOT EXISTS idx_proxy_records_session ON proxy_records(session, idx);
CREATE INDEX IF NOT EXISTS idx_proxy_records_ts ON proxy_records(ts);

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
    finish_reason TEXT,
    UNIQUE(session)
);
CREATE INDEX IF NOT EXISTS idx_gateway_logs_ts ON gateway_logs(ts);
CREATE INDEX IF NOT EXISTS idx_gateway_logs_model ON gateway_logs(model);
