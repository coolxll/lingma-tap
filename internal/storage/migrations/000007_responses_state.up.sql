-- Migration 000007: Add responses_state table for multi-turn conversation support
-- Stores response state for 24 hours to enable previous_response_id chaining

CREATE TABLE IF NOT EXISTS responses_state (
    response_id TEXT PRIMARY KEY,
    parent_id TEXT NOT NULL DEFAULT '',
    uid_digest TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'in_progress',
    input_json TEXT NOT NULL DEFAULT '',
    output_json TEXT NOT NULL DEFAULT '',
    response_json TEXT NOT NULL DEFAULT '',
    instructions TEXT NOT NULL DEFAULT '',
    warnings_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_responses_state_parent ON responses_state(parent_id);
CREATE INDEX IF NOT EXISTS idx_responses_state_uid ON responses_state(uid_digest);
CREATE INDEX IF NOT EXISTS idx_responses_state_expires ON responses_state(expires_at);
