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

func TestParseResponse_LingmaJSONStreamWithoutDataPrefix(t *testing.T) {
	req := &http.Request{
		Method: "POST",
		Host:   "api.lingma.aliyun.com",
		URL: &url.URL{
			Scheme: "https",
			Host:   "api.lingma.aliyun.com",
			Path:   "/chat",
		},
	}
	resp := &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{},
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	bodyContent := `{"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[{\"delta\":{\"content\":\"跟你\"},\"index\":0}],\"model\":\"auto\"}","statusCodeValue":200,"statusCode":"OK"}
{"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[{\"delta\":{\"content\":\"打招呼\"},\"index\":0}],\"model\":\"auto\"}","statusCodeValue":200,"statusCode":"OK"}
{"firstTokenDuration":277,"totalDuration":409,"serverDuration":16}
`

	rec := ParseResponse(resp, []byte(bodyContent), "test-session", 1)

	if !rec.IsSSE {
		t.Fatalf("Expected IsSSE to be true for Lingma JSON stream without data: prefix")
	}
	if len(rec.SSEEvents) != 3 {
		t.Fatalf("Expected 3 SSE events, got %d", len(rec.SSEEvents))
	}
	if !strings.Contains(rec.SSEEvents[0].Body, "跟你") || !strings.Contains(rec.SSEEvents[1].Body, "打招呼") {
		t.Fatalf("Expected Lingma envelope bodies to be extracted, got: %+v", rec.SSEEvents)
	}
	if rec.SSEEvents[2].Body != "" {
		t.Fatalf("Expected stats metadata not to populate Body, got: %s", rec.SSEEvents[2].Body)
	}
}

func TestParseResponse_NonStreamingChatJSONIsNotSSE(t *testing.T) {
	req := &http.Request{
		Method: "POST",
		Host:   "api.example.com",
		URL: &url.URL{
			Scheme: "https",
			Host:   "api.example.com",
			Path:   "/v1/chat/completions",
		},
	}
	resp := &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{},
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	bodyContent := `{"choices":[{"message":{"content":"hello"}}],"model":"auto"}`
	rec := ParseResponse(resp, []byte(bodyContent), "test-session", 1)

	if rec.IsSSE {
		t.Fatalf("Expected a single non-streaming chat JSON response not to be marked as SSE")
	}
}
