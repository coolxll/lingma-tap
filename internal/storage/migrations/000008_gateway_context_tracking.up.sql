-- Add context trimming tracking fields to gateway_logs
ALTER TABLE gateway_logs ADD COLUMN context_trimmed INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN context_original_bytes INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN context_trimmed_bytes INTEGER DEFAULT 0;

-- Add Responses API degradation tracking fields
ALTER TABLE gateway_logs ADD COLUMN responses_degraded INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN responses_warnings TEXT DEFAULT '';
