package bridge

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coolxll/lingma-tap/internal/auth"
)

type mockTransport struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func TestLingmaClient_ChatStream(t *testing.T) {
	mockResp := `data: {"headers":{},"body":"{\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}","statusCodeValue":200,"statusCode":"OK"}

data: {"headers":{},"body":"{\"choices\":[{\"delta\":{\"content\":\" world!\"},\"finish_reason\":\"stop\"}]}","statusCodeValue":200,"statusCode":"OK"}

data: {"firstTokenDuration":100,"totalDuration":200,"serverDuration":150,"usage":{"input_tokens":10,"output_tokens":20}}

data: [DONE]

`

	// BuildHeaders is called within ChatStream. We need to ensure it doesn't fail.
	// Looking at BuildHeaders, it uses CosyKey etc.
	session := &auth.Session{
		CosyKey: "test-key",
		UID:     "test-uid",
	}

	client := NewLingmaClient(session)
	client.client.Transport = &mockTransport{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(mockResp)),
				Header:     make(http.Header),
			}, nil
		},
	}

	var events []SSEEvent
	err := client.ChatStream(context.Background(), map[string]any{"messages": []any{}}, func(e SSEEvent) error {
		events = append(events, e)
		return nil
	})

	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}

	if len(events) != 4 {
		t.Errorf("Expected 4 events, got %d", len(events))
	}
	if events[0].Content != "Hello" {
		t.Errorf("Expected 'Hello', got '%s'", events[0].Content)
	}
	if events[1].Content != " world!" || events[1].FinishReason != "stop" {
		t.Errorf("Expected ' world!' and 'stop', got '%s' and '%s'", events[1].Content, events[1].FinishReason)
	}
	if events[2].Type != "finish" || events[2].Usage == nil || events[2].Usage.PromptTokens != 10 {
		t.Errorf("Expected finish event with 10 input tokens, got type=%s, usage=%+v", events[2].Type, events[2].Usage)
	}
	if events[3].Type != "done" {
		t.Errorf("Expected done event, got %s", events[3].Type)
	}
}

func TestParseSSEData(t *testing.T) {
	client := &LingmaClient{}

	tests := []struct {
		name  string
		data  string
		check func(*testing.T, SSEEvent, error)
	}{
		{
			name: "double-json wrapper text",
			data: `{"headers":{},"body":"{\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}","statusCodeValue":200,"statusCode":"OK"}`,
			check: func(t *testing.T, ev SSEEvent, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ev.Type != "data" {
					t.Errorf("expected type data, got %s", ev.Type)
				}
				if ev.Content != "Hello" {
					t.Errorf("expected content 'Hello', got '%s'", ev.Content)
				}
			},
		},
		{
			name: "finish event with usage",
			data: `{"firstTokenDuration":123,"totalDuration":456,"serverDuration":300,"usage":{"input_tokens":10,"output_tokens":5}}`,
			check: func(t *testing.T, ev SSEEvent, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ev.Type != "finish" {
					t.Errorf("expected type finish, got %s", ev.Type)
				}
				if ev.Usage == nil {
					t.Fatalf("expected usage, got nil")
				}
				if ev.Usage.PromptTokens != 10 {
					t.Errorf("expected prompt tokens 10, got %d", ev.Usage.PromptTokens)
				}
				if ev.Usage.CompletionTokens != 5 {
					t.Errorf("expected completion tokens 5, got %d", ev.Usage.CompletionTokens)
				}
			},
		},
		{
			name: "direct openAI format",
			data: `{"choices":[{"delta":{"content":" direct"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
			check: func(t *testing.T, ev SSEEvent, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ev.Type != "data" {
					t.Errorf("expected type data, got %s", ev.Type)
				}
				if ev.Content != " direct" {
					t.Errorf("expected content ' direct', got '%s'", ev.Content)
				}
				if ev.FinishReason != "stop" {
					t.Errorf("expected finish_reason 'stop', got '%s'", ev.FinishReason)
				}
				if ev.Usage == nil {
					t.Fatalf("expected usage, got nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := client.parseSSEData(tt.data)
			tt.check(t, ev, err)
		})
	}
}
