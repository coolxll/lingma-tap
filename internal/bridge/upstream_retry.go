package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	rand "math/rand/v2"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	lingmaUpstreamDefaultMaxAttempts            = 3
	lingmaUpstreamDefaultRetryBaseDelay         = 200 * time.Millisecond
	lingmaUpstreamDefaultFirstActionableTimeout = 0
	lingmaUpstreamMaxRetryDelay                 = 30 * time.Second
	lingmaUpstreamMaxBufferedEventBytes         = 2 * 1024 * 1024
)

type lingmaHTTPError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *lingmaHTTPError) Error() string {
	if e == nil {
		return "lingma API returned an unknown HTTP error"
	}
	body := strings.TrimSpace(e.Body)
	if len(body) > 1024 {
		body = body[:1024] + "..."
	}
	if body == "" {
		return fmt.Sprintf("lingma API returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("lingma API returned HTTP %d: %s", e.StatusCode, body)
}

type lingmaFirstActionableTimeoutError struct {
	Timeout time.Duration
}

func (e *lingmaFirstActionableTimeoutError) Error() string {
	return fmt.Sprintf("lingma upstream produced no actionable output within %s", e.Timeout)
}

type lingmaSSEError struct {
	Type    string
	Message string
}

type LingmaUpstreamStats struct {
	Attempts           int
	RecoveryApplied    bool
	ErrorClass         string
	FirstActionableMS  int64
	ReasoningOnlyBytes int
	RequestedProfile   string
	EffectiveProfile   string
}

type LingmaUpstreamObserver func(LingmaUpstreamStats)

func (e *lingmaSSEError) Error() string {
	if e == nil {
		return "lingma upstream returned an SSE error"
	}
	return fmt.Sprintf("lingma upstream error (%s): %s", orDefault(e.Type, "api_error"), orDefault(e.Message, "unknown error"))
}

func loadLingmaUpstreamRetryConfig() (int, time.Duration, time.Duration) {
	maxAttempts := lingmaUpstreamDefaultMaxAttempts
	if raw := strings.TrimSpace(os.Getenv("LINGMA_UPSTREAM_MAX_ATTEMPTS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 1 && parsed <= 5 {
			maxAttempts = parsed
		}
	}

	retryBaseDelay := parsePositiveDurationEnv("LINGMA_UPSTREAM_RETRY_BASE_DELAY", lingmaUpstreamDefaultRetryBaseDelay)
	firstActionableTimeout := parseOptionalDurationEnv("LINGMA_UPSTREAM_FIRST_ACTIONABLE_TIMEOUT", lingmaUpstreamDefaultFirstActionableTimeout)
	return maxAttempts, retryBaseDelay, firstActionableTimeout
}

func parsePositiveDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseOptionalDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "disabled":
		return 0
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// ChatStream retries failures that happen before any downstream-visible output.
// When the optional first-actionable watchdog is enabled, large gm51model
// reasoning requests hold reasoning-only deltas so a stalled attempt can switch
// to the validated non-reasoning profile without leaking duplicate output.
func (c *LingmaClient) ChatStream(ctx context.Context, body map[string]any, cb func(SSEEvent) error) error {
	return c.ChatStreamObserved(ctx, body, cb, nil)
}

func (c *LingmaClient) ChatStreamObserved(ctx context.Context, body map[string]any, cb func(SSEEvent) error, observer LingmaUpstreamObserver) error {
	if c == nil {
		return errors.New("lingma client is nil")
	}
	if cb == nil {
		return errors.New("stream callback is required")
	}

	maxAttempts := c.maxAttempts
	if maxAttempts < 1 {
		maxAttempts = lingmaUpstreamDefaultMaxAttempts
	}
	retryBaseDelay := c.retryBaseDelay
	if retryBaseDelay <= 0 {
		retryBaseDelay = lingmaUpstreamDefaultRetryBaseDelay
	}

	modelKey := lingmaModelKey(body)
	profile := inspectLingmaRequest(body, modelKey)
	recoveryEligible := c.thinkingRecoveryEnabled && profile.LargeThinking
	startedAt := time.Now()
	stats := LingmaUpstreamStats{
		RequestedProfile: lingmaProfileString(body),
		EffectiveProfile: lingmaProfileString(body),
	}
	notifyLingmaUpstreamObserver(observer, stats)

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		usingRecovery := recoveryEligible && attempt > 0
		attemptBody, err := prepareLingmaAttemptBody(body, attempt, usingRecovery)
		if err != nil {
			return err
		}
		if usingRecovery {
			log.Printf(
				"[bridge] Lingma in-request recovery applied model=%s attempt=%d/%d body_bytes=%d tool_history=%d",
				modelKey,
				attempt+1,
				maxAttempts,
				profile.BodyBytes,
				profile.ToolCalls+profile.ToolResults,
			)
		}
		stats.Attempts = attempt + 1
		stats.RecoveryApplied = stats.RecoveryApplied || usingRecovery
		stats.EffectiveProfile = lingmaProfileString(attemptBody)
		notifyLingmaUpstreamObserver(observer, stats)

		waitForActionable := recoveryEligible && !usingRecovery && c.firstActionableTimeout > 0
		committed, err := c.chatStreamAttempt(ctx, attemptBody, cb, waitForActionable, startedAt, &stats, observer)
		if err == nil {
			return nil
		}
		lastErr = err
		stats.ErrorClass = lingmaUpstreamErrorClass(err)
		notifyLingmaUpstreamObserver(observer, stats)
		if committed || ctx.Err() != nil || !isLingmaRecoveryCandidate(err) || attempt+1 >= maxAttempts {
			return err
		}

		delay := lingmaRetryDelay(retryBaseDelay, attempt, lingmaRetryAfterFromError(err))
		log.Printf(
			"[bridge] retrying Lingma upstream class=%s model=%s attempt=%d/%d recovery_next=%t delay=%s",
			lingmaUpstreamErrorClass(err),
			modelKey,
			attempt+1,
			maxAttempts,
			recoveryEligible,
			delay,
		)
		if err := sleepWithContext(ctx, delay); err != nil {
			return err
		}
	}
	return lastErr
}

func (c *LingmaClient) chatStreamAttempt(ctx context.Context, body map[string]any, cb func(SSEEvent) error, waitForActionable bool, startedAt time.Time, stats *LingmaUpstreamStats, observer LingmaUpstreamObserver) (bool, error) {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var committed atomic.Bool
	var timeoutFired atomic.Bool
	var timer *time.Timer
	if c.firstActionableTimeout > 0 {
		timer = time.AfterFunc(c.firstActionableTimeout, func() {
			if committed.Load() {
				return
			}
			timeoutFired.Store(true)
			cancel()
		})
		defer timer.Stop()
	}

	var pending []SSEEvent
	pendingBytes := 0
	commitPending := func() error {
		if committed.CompareAndSwap(false, true) && timer != nil {
			timer.Stop()
		}
		for _, event := range pending {
			if err := cb(event); err != nil {
				return err
			}
		}
		pending = nil
		pendingBytes = 0
		return nil
	}

	err := c.chatStreamOnce(attemptCtx, body, func(event SSEEvent) error {
		if !committed.Load() && event.HasError && isRetryableLingmaSSEError(event) {
			return &lingmaSSEError{Type: event.ErrorType, Message: event.ErrorMsg}
		}
		if committed.Load() {
			recordLingmaActionable(event, startedAt, stats, observer)
			return cb(event)
		}

		pending = append(pending, event)
		pendingBytes += lingmaEventSize(event)
		if waitForActionable && event.ReasoningContent != "" {
			stats.ReasoningOnlyBytes += len(event.ReasoningContent)
		}
		recordLingmaActionable(event, startedAt, stats, observer)
		if lingmaEventCommitsStream(event, waitForActionable) || pendingBytes >= lingmaUpstreamMaxBufferedEventBytes {
			return commitPending()
		}
		return nil
	})

	if timeoutFired.Load() && ctx.Err() == nil {
		return false, &lingmaFirstActionableTimeoutError{Timeout: c.firstActionableTimeout}
	}
	if err != nil {
		return committed.Load(), err
	}
	if !committed.Load() && len(pending) > 0 {
		if err := commitPending(); err != nil {
			return true, err
		}
	}
	return committed.Load(), nil
}

func recordLingmaActionable(event SSEEvent, startedAt time.Time, stats *LingmaUpstreamStats, observer LingmaUpstreamObserver) {
	if stats == nil || stats.FirstActionableMS > 0 || !lingmaEventIsActionable(event) {
		return
	}
	stats.FirstActionableMS = time.Since(startedAt).Milliseconds()
	if stats.FirstActionableMS == 0 {
		stats.FirstActionableMS = 1
	}
	notifyLingmaUpstreamObserver(observer, *stats)
}

func lingmaEventIsActionable(event SSEEvent) bool {
	return event.Content != "" || len(event.ToolCalls) > 0 || event.FinishReason != "" || event.Type == "finish" || event.Type == "done"
}

func notifyLingmaUpstreamObserver(observer LingmaUpstreamObserver, stats LingmaUpstreamStats) {
	if observer != nil {
		observer(stats)
	}
}

func prepareLingmaAttemptBody(body map[string]any, attempt int, recovery bool) (map[string]any, error) {
	if attempt == 0 {
		return body, nil
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal retry body: %w", err)
	}
	var retryBody map[string]any
	if err := json.Unmarshal(bodyJSON, &retryBody); err != nil {
		return nil, fmt.Errorf("clone retry body: %w", err)
	}

	requestID := newUUID()
	retryBody["request_id"] = requestID
	retryBody["chat_record_id"] = requestID
	retryBody["is_retry"] = true
	if business, ok := retryBody["business"].(map[string]any); ok {
		business["id"] = newUUID()
		business["begin_at"] = time.Now().UnixMilli()
	}
	if recovery {
		applyLingmaThinkingRecovery(retryBody)
	}
	return retryBody, nil
}

func lingmaEventCommitsStream(event SSEEvent, waitForActionable bool) bool {
	if event.Type == "finish" || event.Type == "done" || event.HasError || event.FinishReason != "" {
		return true
	}
	if event.Content != "" || len(event.ToolCalls) > 0 {
		return true
	}
	return !waitForActionable && event.ReasoningContent != ""
}

func lingmaEventSize(event SSEEvent) int {
	size := len(event.Content) + len(event.ReasoningContent) + len(event.ErrorMsg) + len(event.Raw)
	for _, toolCall := range event.ToolCalls {
		size += len(toolCall.ID) + len(toolCall.Name) + len(toolCall.Arguments)
	}
	return size
}

func isRetryableLingmaSSEError(event SSEEvent) bool {
	if !event.HasError {
		return false
	}
	errorType := strings.ToLower(strings.TrimSpace(event.ErrorType))
	for _, marker := range []string{"server", "internal", "overload", "rate", "timeout", "unavailable"} {
		if strings.Contains(errorType, marker) {
			return true
		}
	}
	return false
}

func isLingmaRecoveryCandidate(err error) bool {
	if err == nil || isContextCanceled(nil, err) {
		return false
	}
	if isLingmaFirstActionableTimeout(err) || isLingmaTransportError(err) || isLingmaUpstreamEOF(err) {
		return true
	}
	var httpErr *lingmaHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
			http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
	}
	var sseErr *lingmaSSEError
	return errors.As(err, &sseErr)
}

func isLingmaFirstActionableTimeout(err error) bool {
	var timeoutErr *lingmaFirstActionableTimeoutError
	return errors.As(err, &timeoutErr)
}

func isLingmaTransportError(err error) bool {
	if err == nil || isContextCanceled(nil, err) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"stream error", "internal_error", "rst_stream", "connection reset",
		"broken pipe", "server closed idle connection",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func lingmaUpstreamErrorClass(err error) string {
	switch {
	case isLingmaFirstActionableTimeout(err):
		return "first_actionable_timeout"
	case isLingmaUpstreamEOF(err):
		return "eof"
	case isContextDeadlineExceeded(nil, err):
		return "deadline"
	}
	var httpErr *lingmaHTTPError
	if errors.As(err, &httpErr) {
		return fmt.Sprintf("http_%d", httpErr.StatusCode)
	}
	var sseErr *lingmaSSEError
	if errors.As(err, &sseErr) {
		return "sse_" + orDefault(strings.ToLower(sseErr.Type), "api_error")
	}
	if isLingmaTransportError(err) {
		return "transport"
	}
	return "other"
}

func lingmaModelKey(body map[string]any) string {
	if modelConfig, ok := body["model_config"].(map[string]any); ok {
		if modelKey, ok := modelConfig["key"].(string); ok {
			return modelKey
		}
	}
	return ""
}

func lingmaProfileString(body map[string]any) string {
	if body == nil {
		return ""
	}
	agentID, _ := body["agent_id"].(string)
	taskID, _ := body["task_id"].(string)
	isReasoning := false
	source := ""
	if modelConfig, ok := body["model_config"].(map[string]any); ok {
		isReasoning, _ = modelConfig["is_reasoning"].(bool)
		source, _ = modelConfig["source"].(string)
	}
	return fmt.Sprintf("task=%s;agent=%s;reasoning=%t;source=%s", taskID, agentID, isReasoning, source)
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func lingmaRetryDelay(baseDelay time.Duration, retryIndex int, retryAfter time.Duration) time.Duration {
	maxDelay := baseDelay
	if maxDelay > lingmaUpstreamMaxRetryDelay {
		maxDelay = lingmaUpstreamMaxRetryDelay
	}
	for i := 0; i < retryIndex && maxDelay < lingmaUpstreamMaxRetryDelay; i++ {
		if maxDelay > lingmaUpstreamMaxRetryDelay/2 {
			maxDelay = lingmaUpstreamMaxRetryDelay
			break
		}
		maxDelay *= 2
	}
	delay := time.Duration(0)
	if maxDelay > 0 {
		delay = time.Duration(rand.Int64N(int64(maxDelay) + 1))
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > lingmaUpstreamMaxRetryDelay {
		return lingmaUpstreamMaxRetryDelay
	}
	return delay
}

func lingmaRetryAfterFromError(err error) time.Duration {
	var httpErr *lingmaHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.RetryAfter
	}
	return 0
}

func parseLingmaRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}
