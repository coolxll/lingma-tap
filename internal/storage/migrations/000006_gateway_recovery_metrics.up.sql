ALTER TABLE gateway_logs ADD COLUMN upstream_attempts INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN recovery_applied INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN upstream_error_class TEXT DEFAULT '';
ALTER TABLE gateway_logs ADD COLUMN first_actionable_ms INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN reasoning_only_bytes INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN requested_profile TEXT DEFAULT '';
ALTER TABLE gateway_logs ADD COLUMN effective_profile TEXT DEFAULT '';
