-- Remove context trimming tracking fields from gateway_logs
ALTER TABLE gateway_logs DROP COLUMN responses_warnings;
ALTER TABLE gateway_logs DROP COLUMN responses_degraded;
ALTER TABLE gateway_logs DROP COLUMN context_trimmed_bytes;
ALTER TABLE gateway_logs DROP COLUMN context_original_bytes;
ALTER TABLE gateway_logs DROP COLUMN context_trimmed;
