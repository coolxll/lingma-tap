package proto

import (
	"encoding/json"
	"strings"
)

const (
	// BodyPreviewLimit bounds the body text sent in list and WebSocket records.
	BodyPreviewLimit = 4 * 1024
	// BodyCaptureLimit bounds the retained bytes for one request or response.
	BodyCaptureLimit = 16 * 1024 * 1024
)

// Record represents a parsed HTTP request/response pair for Lingma API traffic.
type Record struct {
	// Metadata
	ID        int64  `json:"id" db:"id"`
	Ts        string `json:"ts" db:"ts"`
	Session   string `json:"session" db:"session"`
	Index     int    `json:"index" db:"idx"`
	Direction string `json:"direction" db:"direction"` // "C2S" or "S2C"
	Source    string `json:"source" db:"source"`       // "proxy" or "gateway"

	// Request
	Method       string            `json:"method" db:"method"`
	URL          string            `json:"url" db:"url"`
	Host         string            `json:"host" db:"host"`
	Path         string            `json:"path" db:"path"`
	IsEncoded    bool              `json:"is_encoded" db:"is_encoded"`
	EndpointType string            `json:"endpoint_type" db:"endpoint_type"` // chat, finish, embedding, tracking, image_upload, image_resource, other
	ReqHeaders   map[string]string `json:"request_headers" db:"-"`
	ReqBody      string            `json:"request_body" db:"req_body"`
	ReqBodyRaw   string            `json:"request_body_raw" db:"req_body_raw"`
	ReqBodyBlob  []byte            `json:"-" db:"req_body_blob"`
	ReqMime      string            `json:"request_mime" db:"req_mime"`
	ReqSize      int64             `json:"request_size" db:"req_size"`

	// Response
	Status       int               `json:"status" db:"status"`
	StatusText   string            `json:"status_text" db:"status_text"`
	RespHeaders  map[string]string `json:"response_headers" db:"-"`
	RespBody     string            `json:"response_body" db:"resp_body"`
	RespBodyRaw  string            `json:"response_body_raw" db:"-"`
	RespBodyBlob []byte            `json:"-" db:"resp_body_blob"`
	RespMime     string            `json:"response_mime" db:"resp_mime"`
	RespSize     int64             `json:"response_size" db:"resp_size"`
	IsSSE        bool              `json:"is_sse" db:"is_sse"`
	SSEEvents    []SSEEvent        `json:"sse_events,omitempty" db:"-"`

	// Body lifecycle and capture metadata. Full bytes are kept in the BLOB
	// columns and fetched on demand rather than sent through list/WS payloads.
	BodyPhase       string   `json:"body_phase,omitempty" db:"body_phase"`
	BodyComplete    bool     `json:"body_complete" db:"body_complete"`
	BodyTruncated   bool     `json:"body_truncated" db:"body_truncated"`
	CapturedSize    int64    `json:"captured_size" db:"captured_size"`
	DeclaredSize    int64    `json:"declared_size,omitempty" db:"declared_size"`
	BodyEncoding    string   `json:"body_encoding,omitempty" db:"body_encoding"`
	ContentEncoding string   `json:"content_encoding,omitempty" db:"content_encoding"`
	CorrelationKeys []string `json:"correlation_keys,omitempty" db:"-"`
	ArtifactIDs     []int64  `json:"artifact_ids,omitempty" db:"-"`

	// Error
	Error string `json:"error,omitempty" db:"error"`

	// AI Metadata (for source === 'gateway')
	Model              string `json:"model,omitempty" db:"-"`
	InputTokens        int    `json:"input_tokens,omitempty" db:"-"`
	OutputTokens       int    `json:"output_tokens,omitempty" db:"-"`
	CachedTokens       int    `json:"cached_tokens,omitempty" db:"-"`
	ReasoningTokens    int    `json:"reasoning_tokens,omitempty" db:"-"`
	TotalTokens        int    `json:"total_tokens,omitempty" db:"-"`
	TTFT               int64  `json:"ttft,omitempty" db:"-"`
	Latency            int64  `json:"latency,omitempty" db:"-"`
	FinishReason       string `json:"finish_reason,omitempty" db:"-"`
	UpstreamAttempts   int    `json:"upstream_attempts,omitempty" db:"-"`
	RecoveryApplied    bool   `json:"recovery_applied,omitempty" db:"-"`
	UpstreamErrorClass string `json:"upstream_error_class,omitempty" db:"-"`
	FirstActionableMS  int64  `json:"first_actionable_ms,omitempty" db:"-"`
	ReasoningOnlyBytes int    `json:"reasoning_only_bytes,omitempty" db:"-"`
	RequestedProfile   string `json:"requested_profile,omitempty" db:"-"`
	EffectiveProfile   string `json:"effective_profile,omitempty" db:"-"`

	// DB Helpers (not exported to JSON if not needed, but here they are for sqlx)
	ReqHeadersJSON      string `json:"-" db:"req_headers_json"`
	RespHeadersJSON     string `json:"-" db:"resp_headers_json"`
	SSEEventsJSON       string `json:"-" db:"sse_events_json"`
	CorrelationKeysJSON string `json:"-" db:"correlation_keys_json"`
	ArtifactIDsJSON     string `json:"-" db:"artifact_ids_json"`
	RawJSON             string `json:"-" db:"raw_json"`
}

// Artifact is a binary part extracted from a captured request, such as the
// file part of Lingma's image upload multipart body.
type Artifact struct {
	ID       int64  `json:"id" db:"id"`
	RecordID int64  `json:"record_id" db:"record_id"`
	Field    string `json:"field_name,omitempty" db:"field_name"`
	Filename string `json:"filename,omitempty" db:"filename"`
	MIME     string `json:"mime,omitempty" db:"mime"`
	Size     int64  `json:"size" db:"size"`
	SHA256   string `json:"sha256,omitempty" db:"sha256"`
}

// SSEEvent represents a single Server-Sent Event.
type SSEEvent struct {
	EventType string `json:"event_type" db:"event_type"`
	Data      string `json:"data" db:"data"`
	Body      string `json:"body,omitempty" db:"body"`
}

// GatewayLog represents a structured log entry for AI Gateway traffic.
type GatewayLog struct {
	ID                   int64      `json:"id" db:"id"`
	Ts                   string     `json:"ts" db:"ts"`
	Session              string     `json:"session" db:"session"`
	Model                string     `json:"model" db:"model"`
	Method               string     `json:"method" db:"method"`
	Path                 string     `json:"path" db:"path"`
	RequestBody          string     `json:"request_body" db:"request_body"`
	ResponseBody         string     `json:"response_body" db:"response_body"`
	InputTokens          int        `json:"input_tokens" db:"input_tokens"`
	OutputTokens         int        `json:"output_tokens" db:"output_tokens"`
	CachedTokens         int        `json:"cached_tokens,omitempty" db:"cached_tokens"`
	ReasoningTokens      int        `json:"reasoning_tokens,omitempty" db:"reasoning_tokens"`
	TotalTokens          int        `json:"total_tokens,omitempty" db:"total_tokens"`
	TTFT                 int64      `json:"ttft,omitempty" db:"ttft"` // ms to first token
	UpstreamAttempts     int        `json:"upstream_attempts,omitempty" db:"upstream_attempts"`
	RecoveryApplied      bool       `json:"recovery_applied,omitempty" db:"recovery_applied"`
	UpstreamErrorClass   string     `json:"upstream_error_class,omitempty" db:"upstream_error_class"`
	FirstActionableMS    int64      `json:"first_actionable_ms,omitempty" db:"first_actionable_ms"`
	ReasoningOnlyBytes   int        `json:"reasoning_only_bytes,omitempty" db:"reasoning_only_bytes"`
	RequestedProfile     string     `json:"requested_profile,omitempty" db:"requested_profile"`
	EffectiveProfile     string     `json:"effective_profile,omitempty" db:"effective_profile"`
	ContextTrimmed       bool       `json:"context_trimmed,omitempty" db:"context_trimmed"`
	ContextOriginalBytes int        `json:"context_original_bytes,omitempty" db:"context_original_bytes"`
	ContextTrimmedBytes  int        `json:"context_trimmed_bytes,omitempty" db:"context_trimmed_bytes"`
	ResponsesDegraded    bool       `json:"responses_degraded,omitempty" db:"responses_degraded"`
	ResponsesWarnings    string     `json:"responses_warnings,omitempty" db:"responses_warnings"`
	Status               int        `json:"status" db:"status"`
	Latency              int64      `json:"latency" db:"latency"` // ms
	Error                string     `json:"error,omitempty" db:"error"`
	IsSSE                bool       `json:"is_sse" db:"is_sse"`
	SSEEvents            []SSEEvent `json:"sse_events,omitempty" db:"-"`
	FinishReason         string     `json:"finish_reason,omitempty" db:"finish_reason"`

	// DB Helpers
	SSEEventsJSON string `json:"-" db:"sse_events_json"`
}

// GatewayLogStats represents aggregate statistics for AI Gateway logs.
type GatewayLogStats struct {
	Total           int   `json:"total" db:"total"`
	InputTokens     int64 `json:"input_tokens" db:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens" db:"output_tokens"`
	CachedTokens    int64 `json:"cached_tokens" db:"cached_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens" db:"reasoning_tokens"`
	TotalTokens     int64 `json:"total_tokens" db:"total_tokens"`
}

// Session represents an aggregated view of a request/response pair.
type Session struct {
	ID           string `json:"id" db:"id"`
	Host         string `json:"host" db:"host"`
	Path         string `json:"path" db:"path"`
	EndpointType string `json:"endpoint_type" db:"endpoint_type"`
	RecordCount  int    `json:"record_count" db:"record_count"`
	FirstTs      string `json:"first_ts" db:"first_ts"`
	LastTs       string `json:"last_ts" db:"last_ts"`
	ReqSize      int64  `json:"request_size" db:"req_size"`
	RespSize     int64  `json:"response_size" db:"resp_size"`
	Preview      string `json:"preview" db:"preview"`
}

// ToJSON serializes a record to JSON bytes.
func (r *Record) ToJSON() []byte {
	b, _ := json.Marshal(r)
	return b
}

// Endpoint types
const (
	EndpointChat          = "chat"
	EndpointFinish        = "finish"
	EndpointEmbedding     = "embedding"
	EndpointTracking      = "tracking"
	EndpointImageUpload   = "image_upload"
	EndpointImageResource = "image_resource"
	EndpointOther         = "other"
)

// ClassifyEndpoint determines the endpoint type from a URL path.
func ClassifyEndpoint(path string) string {
	switch {
	case contains(path, "/image/upload"):
		return EndpointImageUpload
	case contains(path, "agent_chat_generation"):
		return EndpointChat
	case contains(path, "business/finish"):
		return EndpointFinish
	case contains(path, "embedding"):
		return EndpointEmbedding
	case contains(path, "tracking"):
		return EndpointTracking
	default:
		return EndpointOther
	}
}

// PreviewText returns a bounded UTF-8-safe body preview for list/search views.
func PreviewText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	limit := len(body)
	if limit > BodyPreviewLimit {
		limit = BodyPreviewLimit
	}
	text := strings.ToValidUTF8(string(body[:limit]), "�")
	if len(body) > limit {
		text += "…"
	}
	return text
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
