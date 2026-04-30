package proto

import "encoding/json"

// Record represents a parsed HTTP request/response pair for Lingma API traffic.
type Record struct {
	// Metadata
	ID        int64  `json:"id"`
	Ts        string `json:"ts"`
	Session   string `json:"session"`
	Index     int    `json:"index"`
	Direction string `json:"direction"` // "C2S" or "S2C"
	Source    string `json:"source"`    // "proxy" or "gateway"

	// Request
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Host         string            `json:"host"`
	Path         string            `json:"path"`
	IsEncoded    bool              `json:"is_encoded"`
	EndpointType string            `json:"endpoint_type"` // chat, finish, embedding, tracking, other
	ReqHeaders   map[string]string `json:"request_headers"`
	ReqBody      string            `json:"request_body"`
	ReqBodyRaw   string            `json:"request_body_raw"`
	ReqMime      string            `json:"request_mime"`
	ReqSize      int64             `json:"request_size"`

	// Response
	Status      int               `json:"status"`
	StatusText  string            `json:"status_text"`
	RespHeaders map[string]string `json:"response_headers"`
	RespBody    string            `json:"response_body"`
	RespMime    string            `json:"response_mime"`
	RespSize    int64             `json:"response_size"`
	IsSSE       bool              `json:"is_sse"`
	SSEEvents   []SSEEvent        `json:"sse_events,omitempty"`

	// Error
	Error string `json:"error,omitempty"`

	// AI Metadata (for source === 'gateway')
	Model        string `json:"model,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	Latency      int64  `json:"latency,omitempty"`
}

// SSEEvent represents a single Server-Sent Event.
type SSEEvent struct {
	EventType string `json:"event_type"`
	Data      string `json:"data"`
	Body      string `json:"body,omitempty"`
}

// GatewayLog represents a structured log entry for AI Gateway traffic.
type GatewayLog struct {
	ID           int64      `json:"id"`
	Ts           string     `json:"ts"`
	Session      string     `json:"session"`
	Model        string     `json:"model"`
	Method       string     `json:"method"`
	Path         string     `json:"path"`
	RequestBody  string     `json:"request_body"`
	ResponseBody string     `json:"response_body"`
	InputTokens  int        `json:"input_tokens"`
	OutputTokens int        `json:"output_tokens"`
	Status       int        `json:"status"`
	Latency      int64      `json:"latency"` // ms
	Error        string     `json:"error,omitempty"`
	IsSSE        bool       `json:"is_sse"`
	SSEEvents    []SSEEvent `json:"sse_events,omitempty"`
}

// Session represents an aggregated view of a request/response pair.
type Session struct {
	ID           string `json:"id"`
	Host         string `json:"host"`
	Path         string `json:"path"`
	EndpointType string `json:"endpoint_type"`
	RecordCount  int    `json:"record_count"`
	FirstTs      string `json:"first_ts"`
	LastTs       string `json:"last_ts"`
	ReqSize      int64  `json:"request_size"`
	RespSize     int64  `json:"response_size"`
	Preview      string `json:"preview"`
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
