package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
)

func TestLingmaClientRetriesHTTP5xxBeforeOutput(t *testing.T) {
	client := newRetryTestClient()
	var calls atomic.Int32
	client.client.Transport = &mockTransport{roundTripFunc: func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return retryTestResponse(http.StatusBadGateway, `{"error":"temporary"}`), nil
		}
		return retryTestResponse(http.StatusOK, successLingmaStreamBody()), nil
	}}

	var content strings.Builder
	err := client.ChatStream(context.Background(), retryTestBody(false), func(event SSEEvent) error {
		content.WriteString(event.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
	if content.String() != "ok" {
		t.Fatalf("content = %q, want ok", content.String())
	}
}

func TestLingmaClientRetryRefreshesRequestIdentity(t *testing.T) {
	originalUUIDGenerator := uuidGenerator
	var uuidCounter atomic.Int32
	uuidGenerator = func() string {
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", uuidCounter.Add(1))
	}
	t.Cleanup(func() { uuidGenerator = originalUUIDGenerator })

	client := newRetryTestClient()
	var payloads []map[string]any
	client.client.Transport = &mockTransport{roundTripFunc: func(req *http.Request) (*http.Response, error) {
		payload, err := decodeLingmaRequestBody(req)
		if err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		if len(payloads) == 1 {
			return nil, io.EOF
		}
		return retryTestResponse(http.StatusOK, successLingmaStreamBody()), nil
	}}

	if err := client.ChatStream(context.Background(), retryTestBody(false), func(SSEEvent) error { return nil }); err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	if len(payloads) != 2 {
		t.Fatalf("payload count = %d, want 2", len(payloads))
	}
	if payloads[0]["request_id"] == payloads[1]["request_id"] {
		t.Fatalf("retry reused request_id %v", payloads[0]["request_id"])
	}
	if payloads[0]["chat_record_id"] == payloads[1]["chat_record_id"] {
		t.Fatalf("retry reused chat_record_id %v", payloads[0]["chat_record_id"])
	}
	if payloads[0]["session_id"] != payloads[1]["session_id"] {
		t.Fatalf("retry changed session_id: %v -> %v", payloads[0]["session_id"], payloads[1]["session_id"])
	}
	if retried, _ := payloads[1]["is_retry"].(bool); !retried {
		t.Fatalf("retry body is_retry = %v, want true", payloads[1]["is_retry"])
	}
	firstBusiness, _ := payloads[0]["business"].(map[string]any)
	secondBusiness, _ := payloads[1]["business"].(map[string]any)
	if firstBusiness["id"] == secondBusiness["id"] {
		t.Fatal("retry reused business.id")
	}
	if beginAt, _ := secondBusiness["begin_at"].(float64); beginAt <= 0 {
		t.Fatalf("retry business.begin_at = %v, want positive timestamp", secondBusiness["begin_at"])
	}
}

func TestLingmaStreamingClientUsesHTTP11WithoutGlobalTimeout(t *testing.T) {
	client := NewLingmaClient(&auth.Session{CosyKey: "test-key", UID: "test-uid"})
	if client.client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want disabled for streaming", client.client.Timeout)
	}
	transport, ok := client.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.client.Transport)
	}
	if transport.ForceAttemptHTTP2 {
		t.Fatal("Lingma transport unexpectedly enables HTTP/2")
	}
	if transport.TLSClientConfig == nil || len(transport.TLSClientConfig.NextProtos) != 1 || transport.TLSClientConfig.NextProtos[0] != "http/1.1" {
		t.Fatalf("ALPN protocols = %#v, want [http/1.1]", transport.TLSClientConfig)
	}
}

func TestLingmaRetryConfigDisablesWatchdogByDefault(t *testing.T) {
	t.Setenv("LINGMA_UPSTREAM_MAX_ATTEMPTS", "")
	t.Setenv("LINGMA_UPSTREAM_RETRY_BASE_DELAY", "")
	t.Setenv("LINGMA_UPSTREAM_FIRST_ACTIONABLE_TIMEOUT", "")
	attempts, delay, timeout := loadLingmaUpstreamRetryConfig()
	if attempts != lingmaUpstreamDefaultMaxAttempts || delay != lingmaUpstreamDefaultRetryBaseDelay {
		t.Fatalf("retry defaults = (%d, %s)", attempts, delay)
	}
	if timeout != 0 {
		t.Fatalf("first actionable timeout = %s, want disabled", timeout)
	}
}

func TestLingmaRetryAfterAndJitter(t *testing.T) {
	now := time.Date(2026, time.July, 12, 0, 0, 0, 0, time.UTC)
	if got := parseLingmaRetryAfter("3", now); got != 3*time.Second {
		t.Fatalf("delta Retry-After = %s, want 3s", got)
	}
	if got := parseLingmaRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); got != 5*time.Second {
		t.Fatalf("date Retry-After = %s, want 5s", got)
	}
	for i := 0; i < 32; i++ {
		delay := lingmaRetryDelay(200*time.Millisecond, 0, 0)
		if delay < 0 || delay > 200*time.Millisecond {
			t.Fatalf("jitter delay = %s, want [0, 200ms]", delay)
		}
	}
	if got := lingmaRetryDelay(200*time.Millisecond, 0, 750*time.Millisecond); got != 750*time.Millisecond {
		t.Fatalf("Retry-After delay = %s, want 750ms", got)
	}
	if got := lingmaRetryDelay(20*time.Second, 4, time.Minute); got != lingmaUpstreamMaxRetryDelay {
		t.Fatalf("capped delay = %s, want %s", got, lingmaUpstreamMaxRetryDelay)
	}
}

func TestLingmaRetryableSSEErrorsAreConservative(t *testing.T) {
	for _, errorType := range []string{"server_error", "overloaded_error", "rate_limit_error", "upstream_timeout"} {
		if !isRetryableLingmaSSEError(SSEEvent{HasError: true, ErrorType: errorType}) {
			t.Fatalf("error type %q should be retryable", errorType)
		}
	}
	for _, errorType := range []string{"api_error", "invalid_request_error", "authentication_error", ""} {
		if isRetryableLingmaSSEError(SSEEvent{HasError: true, ErrorType: errorType}) {
			t.Fatalf("error type %q should not be retryable", errorType)
		}
	}
}

func TestLingmaTransportRetryClassificationIsConservative(t *testing.T) {
	if isLingmaTransportError(&net.DNSError{Err: "no such host", Name: "invalid.example", IsNotFound: true}) {
		t.Fatal("permanent DNS failure should not be retried")
	}
	if !isLingmaTransportError(&net.DNSError{Err: "timeout", Name: "slow.example", IsTimeout: true}) {
		t.Fatal("DNS timeout should be retried")
	}
	if !isLingmaTransportError(io.ErrUnexpectedEOF) {
		t.Fatal("unexpected EOF should be retried")
	}
}

func TestLingmaClientDoesNotRetryAfterVisibleOutput(t *testing.T) {
	client := newRetryTestClient()
	var calls atomic.Int32
	client.client.Transport = &mockTransport{roundTripFunc: func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return retryTestResponse(http.StatusOK, `data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n"), nil
	}}

	var content strings.Builder
	err := client.ChatStream(context.Background(), retryTestBody(false), func(event SSEEvent) error {
		content.WriteString(event.Content)
		return nil
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ChatStream error = %v, want unexpected EOF", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
	if content.String() != "partial" {
		t.Fatalf("content = %q, want partial", content.String())
	}
}

func TestLingmaClientLargeReasoningTimeoutUsesRecoveryProfile(t *testing.T) {
	client := newRetryTestClient()
	client.maxAttempts = 2
	client.firstActionableTimeout = 250 * time.Millisecond
	client.thinkingRecoveryEnabled = true

	var calls atomic.Int32
	var payloads []map[string]any
	client.client.Transport = &mockTransport{roundTripFunc: func(req *http.Request) (*http.Response, error) {
		payload, err := decodeLingmaRequestBody(req)
		if err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		if calls.Add(1) == 1 {
			reader, writer := io.Pipe()
			go func() {
				_, _ = io.WriteString(writer, `data: {"choices":[{"delta":{"reasoning_content":"stuck reasoning"}}]}`+"\n\n")
				<-req.Context().Done()
				_ = writer.CloseWithError(req.Context().Err())
			}()
			return &http.Response{StatusCode: http.StatusOK, Body: reader, Header: make(http.Header)}, nil
		}
		return retryTestResponse(http.StatusOK, successLingmaStreamBody()), nil
	}}

	var reasoning, content strings.Builder
	var observed LingmaUpstreamStats
	err := client.ChatStreamObserved(context.Background(), largeRetryTestBody(), func(event SSEEvent) error {
		reasoning.WriteString(event.ReasoningContent)
		content.WriteString(event.Content)
		return nil
	}, func(stats LingmaUpstreamStats) {
		observed = stats
	})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
	if reasoning.Len() != 0 {
		t.Fatalf("failed attempt reasoning leaked downstream: %q", reasoning.String())
	}
	if content.String() != "ok" {
		t.Fatalf("content = %q, want ok", content.String())
	}
	if len(payloads) != 2 {
		t.Fatalf("payload count = %d, want 2", len(payloads))
	}
	assertFallbackBody(t, payloads[1], false)
	if payloads[0]["session_id"] != payloads[1]["session_id"] {
		t.Fatalf("recovery changed session_id: %v -> %v", payloads[0]["session_id"], payloads[1]["session_id"])
	}
	if observed.Attempts != 2 || !observed.RecoveryApplied || observed.ErrorClass != "first_actionable_timeout" {
		t.Fatalf("unexpected observed recovery stats: %+v", observed)
	}
	if observed.FirstActionableMS <= 0 || observed.ReasoningOnlyBytes != len("stuck reasoning") {
		t.Fatalf("unexpected actionable/reasoning stats: %+v", observed)
	}
	if observed.RequestedProfile == observed.EffectiveProfile || !strings.Contains(observed.EffectiveProfile, "agent=agent_common") {
		t.Fatalf("effective profile did not record recovery: %+v", observed)
	}
}

func TestLingmaClientParentCancellationDoesNotRetry(t *testing.T) {
	client := newRetryTestClient()
	var calls atomic.Int32
	client.client.Transport = &mockTransport{roundTripFunc: func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, req.Context().Err()
	}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := client.ChatStream(ctx, retryTestBody(false), func(SSEEvent) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ChatStream error = %v, want context canceled", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestLingmaClientExhaustedUpstreamErrorsMapToBadGateway(t *testing.T) {
	client := newRetryTestClient()
	client.maxAttempts = 2
	var calls atomic.Int32
	client.client.Transport = &mockTransport{roundTripFunc: func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("stream error: stream ID 3; INTERNAL_ERROR; received from peer")
	}}

	err := client.ChatStream(context.Background(), retryTestBody(false), func(SSEEvent) error { return nil })
	if err == nil {
		t.Fatal("expected ChatStream error")
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
	if status := statusForLingmaUpstreamError(err); status != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", status, http.StatusBadGateway)
	}
}

func newRetryTestClient() *LingmaClient {
	client := NewLingmaClient(&auth.Session{CosyKey: "test-key", UID: "test-uid"})
	client.maxAttempts = 3
	client.retryBaseDelay = time.Millisecond
	client.firstActionableTimeout = 0
	client.thinkingRecoveryEnabled = false
	return client
}

func retryTestBody(reasoning bool) map[string]any {
	return BuildLingmaBody(
		[]map[string]any{{"role": "user", "content": "ping"}},
		nil,
		"gm51model",
		nil,
		[]byte(`{"model":"gm51model","messages":[{"role":"user","content":"ping"}]}`),
		reasoning,
		nil,
	)
}

func largeRetryTestBody() map[string]any {
	messages := []map[string]any{{"role": "user", "content": strings.Repeat("x", 140*1024)}}
	for i := 0; i < 10; i++ {
		callID := fmt.Sprintf("call_%d", i)
		messages = append(messages,
			map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{{
					"id":   callID,
					"type": "function",
					"function": map[string]any{
						"name":      "read_file",
						"arguments": fmt.Sprintf(`{"index":%d}`, i),
					},
				}},
			},
			map[string]any{"role": "tool", "tool_call_id": callID, "content": "result"},
		)
	}
	return BuildLingmaBody(messages, nil, "gm51model", nil, []byte(`{"agent":"coding"}`), true, nil)
}

func retryTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
