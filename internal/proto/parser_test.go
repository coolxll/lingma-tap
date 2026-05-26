package proto

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestParseResponse_RobustSSE(t *testing.T) {
	// 1. Mock request
	req := &http.Request{
		Method: "POST",
		Host:   "api.coze.com",
		URL: &url.URL{
			Scheme: "https",
			Host:   "api.coze.com",
			Path:   "/v3/chat",
		},
	}

	// 2. Mock response where Content-Type is application/json (incorrect/different),
	// but the body content consists of SSE-formatted data lines.
	resp := &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{},
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	bodyContent := `data: {"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[{\"delta\":{\"content\":\"思考\"}}]}"}

data: {"headers":{"Content-Type":["application/json"]},"body":"[DONE]"}
`

	// 3. Parse response
	rec := ParseResponse(resp, []byte(bodyContent), "test-session", 1)

	// 4. Verification
	if !rec.IsSSE {
		t.Errorf("Expected IsSSE to be true despite Content-Type being application/json, but got false")
	}

	if len(rec.SSEEvents) != 2 {
		t.Fatalf("Expected 2 SSE events, got %d", len(rec.SSEEvents))
	}

	if rec.SSEEvents[0].Data == "" {
		t.Errorf("Expected first SSE event data to not be empty")
	}

	if !strings.Contains(rec.SSEEvents[0].Body, "思考") {
		t.Errorf("Expected first SSE event body to contain '思考', got: %s", rec.SSEEvents[0].Body)
	}
}
