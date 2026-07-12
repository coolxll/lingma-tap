ALTER TABLE gateway_logs DROP COLUMN effective_profile;
ALTER TABLE gateway_logs DROP COLUMN requested_profile;
ALTER TABLE gateway_logs DROP COLUMN reasoning_only_bytes;
ALTER TABLE gateway_logs DROP COLUMN first_actionable_ms;
ALTER TABLE gateway_logs DROP COLUMN upstream_error_class;
ALTER TABLE gateway_logs DROP COLUMN recovery_applied;
ALTER TABLE gateway_logs DROP COLUMN upstream_attempts;
