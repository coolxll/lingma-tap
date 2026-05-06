package proto

import "encoding/json"

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
	EndpointType string            `json:"endpoint_type" db:"endpoint_type"` // chat, finish, embedding, tracking, other
	ReqHeaders   map[string]string `json:"request_headers" db:"-"`
	ReqBody      string            `json:"request_body" db:"req_body"`
	ReqBodyRaw   string            `json:"request_body_raw" db:"req_body_raw"`
	ReqMime      string            `json:"request_mime" db:"req_mime"`
	ReqSize      int64             `json:"request_size" db:"req_size"`

	// Response
	Status      int               `json:"status" db:"status"`
	StatusText  string            `json:"status_text" db:"status_text"`
	RespHeaders map[string]string `json:"response_headers" db:"-"`
	RespBody    string            `json:"response_body" db:"resp_body"`
	RespBodyRaw string            `json:"response_body_raw" db:"-"`
	RespMime    string            `json:"response_mime" db:"resp_mime"`
	RespSize    int64             `json:"response_size" db:"resp_size"`
	IsSSE       bool              `json:"is_sse" db:"is_sse"`
	SSEEvents   []SSEEvent        `json:"sse_events,omitempty" db:"-"`

	// Error
	Error string `json:"error,omitempty" db:"error"`

	// AI Metadata (for source === 'gateway')
	Model        string `json:"model,omitempty" db:"-"`
	InputTokens  int    `json:"input_tokens,omitempty" db:"-"`
	OutputTokens int    `json:"output_tokens,omitempty" db:"-"`
	Latency      int64  `json:"latency,omitempty" db:"-"`
	FinishReason string `json:"finish_reason,omitempty" db:"-"`

	// DB Helpers (not exported to JSON if not needed, but here they are for sqlx)
	ReqHeadersJSON  string `json:"-" db:"req_headers_json"`
	RespHeadersJSON string `json:"-" db:"resp_headers_json"`
	SSEEventsJSON   string `json:"-" db:"sse_events_json"`
	RawJSON         string `json:"-" db:"raw_json"`
}

// SSEEvent represents a single Server-Sent Event.
type SSEEvent struct {
	EventType string `json:"event_type" db:"event_type"`
	Data      string `json:"data" db:"data"`
	Body      string `json:"body,omitempty" db:"body"`
}

// GatewayLog represents a structured log entry for AI Gateway traffic.
type GatewayLog struct {
	ID           int64      `json:"id" db:"id"`
	Ts           string     `json:"ts" db:"ts"`
	Session      string     `json:"session" db:"session"`
	Model        string     `json:"model" db:"model"`
	Method       string     `json:"method" db:"method"`
	Path         string     `json:"path" db:"path"`
	RequestBody  string     `json:"request_body" db:"request_body"`
	ResponseBody string     `json:"response_body" db:"response_body"`
	InputTokens  int        `json:"input_tokens" db:"input_tokens"`
	OutputTokens int        `json:"output_tokens" db:"output_tokens"`
	Status       int        `json:"status" db:"status"`
	Latency      int64      `json:"latency" db:"latency"` // ms
	Error        string     `json:"error,omitempty" db:"error"`
	IsSSE        bool       `json:"is_sse" db:"is_sse"`
	SSEEvents    []SSEEvent `json:"sse_events,omitempty" db:"-"`
	FinishReason string     `json:"finish_reason,omitempty" db:"finish_reason"`

	// DB Helpers
	SSEEventsJSON string `json:"-" db:"sse_events_json"`
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
	EndpointChat      = "chat"
	EndpointFinish    = "finish"
	EndpointEmbedding = "embedding"
	EndpointTracking  = "tracking"
	EndpointOther     = "other"
)

// ClassifyEndpoint determines the endpoint type from a URL path.
func ClassifyEndpoint(path string) string {
	switch {
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
