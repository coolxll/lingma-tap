ALTER TABLE proxy_records ADD COLUMN req_body_blob BLOB;
ALTER TABLE proxy_records ADD COLUMN resp_body_blob BLOB;
ALTER TABLE proxy_records ADD COLUMN body_phase TEXT DEFAULT 'complete';
ALTER TABLE proxy_records ADD COLUMN body_complete INTEGER DEFAULT 1;
ALTER TABLE proxy_records ADD COLUMN body_truncated INTEGER DEFAULT 0;
ALTER TABLE proxy_records ADD COLUMN captured_size INTEGER DEFAULT 0;
ALTER TABLE proxy_records ADD COLUMN declared_size INTEGER DEFAULT 0;
ALTER TABLE proxy_records ADD COLUMN body_encoding TEXT;
ALTER TABLE proxy_records ADD COLUMN content_encoding TEXT;
ALTER TABLE proxy_records ADD COLUMN correlation_keys_json TEXT;
ALTER TABLE proxy_records ADD COLUMN artifact_ids_json TEXT;

-- Preserve legacy text bodies for databases created before binary capture.
-- req_body_raw is preferred when it exists because it is the closest legacy
-- representation of the wire body; response bodies had no separate raw
-- column and are copied from resp_body.
UPDATE proxy_records
SET req_body_blob = CAST(
    CASE
        WHEN req_body_raw IS NOT NULL AND length(req_body_raw) > 0 THEN req_body_raw
        ELSE req_body
    END AS BLOB),
    body_phase = 'complete',
    body_complete = 1,
    captured_size = length(CAST(
        CASE
            WHEN req_body_raw IS NOT NULL AND length(req_body_raw) > 0 THEN req_body_raw
            ELSE req_body
        END AS BLOB)),
    body_encoding = 'text'
WHERE req_body_blob IS NULL
  AND (req_body IS NOT NULL OR req_body_raw IS NOT NULL);

UPDATE proxy_records
SET resp_body_blob = CAST(resp_body AS BLOB),
    body_phase = 'complete',
    body_complete = 1,
    captured_size = CASE
        WHEN length(CAST(resp_body AS BLOB)) > captured_size THEN length(CAST(resp_body AS BLOB))
        ELSE captured_size
    END,
    body_encoding = CASE
        WHEN body_encoding IS NULL OR body_encoding = '' THEN 'text'
        ELSE body_encoding
    END
WHERE resp_body_blob IS NULL
  AND resp_body IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_proxy_records_endpoint_ts
    ON proxy_records(endpoint_type, ts DESC);

CREATE TABLE IF NOT EXISTS record_artifacts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    record_id   INTEGER NOT NULL,
    field_name  TEXT,
    filename    TEXT,
    mime        TEXT,
    size        INTEGER NOT NULL DEFAULT 0,
    sha256      TEXT,
    body        BLOB NOT NULL,
    created_at  TEXT NOT NULL,
    FOREIGN KEY(record_id) REFERENCES proxy_records(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_record_artifacts_record ON record_artifacts(record_id);

CREATE TABLE IF NOT EXISTS record_correlation_keys (
    record_id   INTEGER NOT NULL,
    key_type    TEXT NOT NULL,
    key_value   TEXT NOT NULL,
    PRIMARY KEY(record_id, key_type, key_value),
    FOREIGN KEY(record_id) REFERENCES proxy_records(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_record_correlation_value
    ON record_correlation_keys(key_type, key_value);
