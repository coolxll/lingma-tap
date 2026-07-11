UPDATE gateway_logs
SET status = 200,
    error = ''
WHERE status = 499
  AND is_sse = 1
  AND trim(COALESCE(finish_reason, '')) <> '';
