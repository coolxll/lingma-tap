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

	// Events are now split: content and finish_reason come as separate events
	// Expected: [content("Hello"), content(" world!"), finish_reason("stop"), finish(usage), done]
	if len(events) < 4 {
		t.Errorf("Expected at least 4 events, got %d", len(events))
		for i, e := range events {
			t.Logf("  [%d] Type=%s Content=%q FinishReason=%q", i, e.Type, e.Content, e.FinishReason)
		}
		t.FailNow()
	}

	// Find events by type/content rather than assuming exact order
	foundContent := false
	foundFinishReason := false
	foundFinish := false
	foundDone := false
	for _, e := range events {
		if e.Content == "Hello" {
			foundContent = true
		}
		if e.Content == " world!" {
			foundContent = true
		}
		if e.FinishReason == "stop" {
			foundFinishReason = true
		}
		if e.Type == "finish" && e.Usage != nil && e.Usage.PromptTokens == 10 {
			foundFinish = true
		}
		if e.Type == "done" {
			foundDone = true
		}
	}
	if !foundContent {
		t.Error("Expected content events not found")
	}
	if !foundFinishReason {
		t.Error("Expected finish_reason 'stop' not found")
	}
	if !foundFinish {
		t.Error("Expected finish event with 10 input tokens not found")
	}
	if !foundDone {
		t.Error("Expected done event not found")
	}
}

func TestParseSSEData(t *testing.T) {
	client := &LingmaClient{}

	tests := []struct {
		name  string
		data  string
		check func(*testing.T, []SSEEvent, error)
	}{
		{
			name: "double-json wrapper text",
			data: `{"headers":{},"body":"{\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}","statusCodeValue":200,"statusCode":"OK"}`,
			check: func(t *testing.T, events []SSEEvent, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(events) == 0 {
					t.Fatal("expected at least 1 event, got 0")
				}
				if events[0].Type != "data" {
					t.Errorf("expected type data, got %s", events[0].Type)
				}
				if events[0].Content != "Hello" {
					t.Errorf("expected content 'Hello', got '%s'", events[0].Content)
				}
			},
		},
		{
			name: "finish event with usage",
			data: `{"firstTokenDuration":123,"totalDuration":456,"serverDuration":300,"usage":{"input_tokens":10,"output_tokens":5}}`,
			check: func(t *testing.T, events []SSEEvent, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(events) == 0 {
					t.Fatal("expected at least 1 event, got 0")
				}
				if events[0].Type != "finish" {
					t.Errorf("expected type finish, got %s", events[0].Type)
				}
				if events[0].Usage == nil {
					t.Fatalf("expected usage, got nil")
				}
				if events[0].Usage.PromptTokens != 10 {
					t.Errorf("expected prompt tokens 10, got %d", events[0].Usage.PromptTokens)
				}
				if events[0].Usage.CompletionTokens != 5 {
					t.Errorf("expected completion tokens 5, got %d", events[0].Usage.CompletionTokens)
				}
			},
		},
		{
			name: "direct openAI format",
			data: `{"choices":[{"delta":{"content":" direct"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
			check: func(t *testing.T, events []SSEEvent, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(events) == 0 {
					t.Fatal("expected at least 1 event, got 0")
				}
				// Should have content event and finish_reason event
				foundContent := false
				foundFinish := false
				var usageEv *SSEEvent
				for i := range events {
					if events[i].Content == " direct" {
						foundContent = true
					}
					if events[i].FinishReason == "stop" {
						foundFinish = true
					}
					if events[i].Usage != nil {
						usageEv = &events[i]
					}
				}
				if !foundContent {
					t.Errorf("expected content ' direct' in events")
				}
				if !foundFinish {
					t.Errorf("expected finish_reason 'stop' in events")
				}
				if usageEv == nil {
					t.Fatalf("expected usage in events, got nil")
				}
			},
		},
		{
			name: "double-json wrapper with nested usage",
			data: `{"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[],\"created\":1779808522,\"id\":\"chatcmpl-a4e7b4f5-4f18-9450-8783-2fe34710c558\",\"model\":\"auto\",\"object\":\"chat.completion.chunk\",\"usage\":{\"completion_tokens\":64,\"prompt_tokens\":7785,\"total_tokens\":7849}}","statusCodeValue":200,"statusCode":"OK"}`,
			check: func(t *testing.T, events []SSEEvent, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(events) == 0 {
					t.Fatal("expected at least 1 event, got 0")
				}
				// Find usage in events
				var usageEv *SSEEvent
				for i := range events {
					if events[i].Usage != nil {
						usageEv = &events[i]
						break
					}
				}
				if usageEv == nil {
					t.Fatalf("expected usage in events, got nil")
				}
				if usageEv.Usage.PromptTokens != 7785 {
					t.Errorf("expected prompt tokens 7785, got %d", usageEv.Usage.PromptTokens)
				}
				if usageEv.Usage.CompletionTokens != 64 {
					t.Errorf("expected completion tokens 64, got %d", usageEv.Usage.CompletionTokens)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &streamState{}
			events, err := client.parseSSEData(tt.data, state)
			tt.check(t, events, err)
		})
	}
}

func TestSplitThoughtTags(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		inThought bool
		wantContent   []string
		wantReasoning []string
		wantInThought bool
	}{
		{
			name:          "no tags",
			content:       "Hello world",
			wantContent:   []string{"Hello world"},
			wantReasoning: nil,
		},
		{
			name:          "complete thought block",
			content:       "before<thought>reasoning</thought>after",
			wantContent:   []string{"before", "after"},
			wantReasoning: []string{"reasoning"},
		},
		{
			name:          "thought at start",
			content:       "<thought>thinking</thought>answer",
			wantContent:   []string{"answer"},
			wantReasoning: []string{"thinking"},
		},
		{
			name:          "thought at end",
			content:       "text<thought>reason</thought>",
			wantContent:   []string{"text"},
			wantReasoning: []string{"reason"},
		},
		{
			name:          "only thought",
			content:       "<thought>deep thinking</thought>",
			wantContent:   nil,
			wantReasoning: []string{"deep thinking"},
		},
		{
			name:          "open thought tag spans chunk",
			content:       "before<thought>partial",
			wantContent:   []string{"before"},
			wantReasoning: []string{"partial"},
			wantInThought: true,
		},
		{
			name:          "close thought tag from previous chunk",
			content:       "continued</thought>after",
			inThought:     true,
			wantContent:   []string{"after"},
			wantReasoning: []string{"continued"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &streamState{inThought: tt.inThought}
			events := s.splitThoughtTags(tt.content)

			var gotContent, gotReasoning []string
			for _, ev := range events {
				if ev.Content != "" {
					gotContent = append(gotContent, ev.Content)
				}
				if ev.ReasoningContent != "" {
					gotReasoning = append(gotReasoning, ev.ReasoningContent)
				}
			}

			if len(gotContent) != len(tt.wantContent) {
				t.Errorf("content count: got %d, want %d: %v", len(gotContent), len(tt.wantContent), gotContent)
			} else {
				for i := range gotContent {
					if gotContent[i] != tt.wantContent[i] {
						t.Errorf("content[%d]: got %q, want %q", i, gotContent[i], tt.wantContent[i])
					}
				}
			}

			if len(gotReasoning) != len(tt.wantReasoning) {
				t.Errorf("reasoning count: got %d, want %d: %v", len(gotReasoning), len(tt.wantReasoning), gotReasoning)
			} else {
				for i := range gotReasoning {
					if gotReasoning[i] != tt.wantReasoning[i] {
						t.Errorf("reasoning[%d]: got %q, want %q", i, gotReasoning[i], tt.wantReasoning[i])
					}
				}
			}

			if s.inThought != tt.wantInThought {
				t.Errorf("inThought: got %v, want %v", s.inThought, tt.wantInThought)
			}
		})
	}
}

func TestParseSSEData_ReasoningContent(t *testing.T) {
	client := &LingmaClient{}

	// Test native reasoning_content field
	data := `{"choices":[{"delta":{"reasoning_content":"let me think..."}}]}`
	state := &streamState{}
	events, err := client.parseSSEData(data, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, ev := range events {
		if ev.ReasoningContent == "let me think..." {
			found = true
		}
	}
	if !found {
		t.Error("expected reasoning_content 'let me think...' not found in events")
	}
}

func TestParseSSEData_ErrorDetection(t *testing.T) {
	client := &LingmaClient{}

	data := `{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`
	state := &streamState{}
	events, err := client.parseSSEData(data, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 event")
	}
	if !events[0].HasError {
		t.Error("expected HasError=true")
	}
	if events[0].ErrorMsg != "rate limit exceeded" {
		t.Errorf("expected error message 'rate limit exceeded', got %q", events[0].ErrorMsg)
	}
	if events[0].ErrorType != "rate_limit_error" {
		t.Errorf("expected error type 'rate_limit_error', got %q", events[0].ErrorType)
	}
}

func TestParseSSEData_ThoughtTagInContent(t *testing.T) {
	client := &LingmaClient{}

	// Test content with embedded <thought> tags via envelope
	data := `{"headers":{},"body":"{\"choices\":[{\"delta\":{\"content\":\"Hello<thought>thinking...</thought>world\"}}]}","statusCodeValue":200,"statusCode":"OK"}`
	state := &streamState{}
	events, err := client.parseSSEData(data, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var contents, reasonings []string
	for _, ev := range events {
		if ev.Content != "" {
			contents = append(contents, ev.Content)
		}
		if ev.ReasoningContent != "" {
			reasonings = append(reasonings, ev.ReasoningContent)
		}
	}

	if len(contents) != 2 || contents[0] != "Hello" || contents[1] != "world" {
		t.Errorf("expected contents [Hello, world], got %v", contents)
	}
	if len(reasonings) != 1 || reasonings[0] != "thinking..." {
		t.Errorf("expected reasonings [thinking...], got %v", reasonings)
	}
}

func TestGenerateSessionID(t *testing.T) {
	input := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	id1 := generateSessionID(input)
	id2 := generateSessionID(input)

	if id1 != id2 {
		t.Errorf("expected deterministic session_id, got %q and %q", id1, id2)
	}
	if len(id1) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %q", len(id1), id1)
	}

	// Different input should produce different ID
	id3 := generateSessionID([]byte(`{"messages":[{"role":"user","content":"different"}]}`))
	if id1 == id3 {
		t.Error("expected different session_ids for different inputs")
	}
}

func TestUsageConsolidate_CachedAndReasoningTokens(t *testing.T) {
	u := &Usage{
		InputTokens:  100,
		OutputTokens: 200,
		PromptTokensDetails: &TokenDetails{
			CachedTokens: 50,
		},
		CompletionTokensDetails: &TokenDetails{
			ReasoningTokens: 30,
		},
	}
	u.Consolidate()

	if u.PromptTokens != 100 {
		t.Errorf("PromptTokens: got %d, want 100", u.PromptTokens)
	}
	if u.CompletionTokens != 200 {
		t.Errorf("CompletionTokens: got %d, want 200", u.CompletionTokens)
	}
	if u.TotalTokens != 300 {
		t.Errorf("TotalTokens: got %d, want 300", u.TotalTokens)
	}
	if u.CachedTokens != 50 {
		t.Errorf("CachedTokens: got %d, want 50", u.CachedTokens)
	}
	if u.ReasoningTokens != 30 {
		t.Errorf("ReasoningTokens: got %d, want 30", u.ReasoningTokens)
	}
}

func TestAnthropicIsReasoning(t *testing.T) {
	tests := []struct {
		name string
		req  map[string]any
		want bool
	}{
		{"no thinking field", map[string]any{}, true},
		{"thinking disabled", map[string]any{"thinking": map[string]any{"type": "disabled"}}, false},
		{"thinking enabled", map[string]any{"thinking": map[string]any{"type": "enabled"}}, true},
		{"thinking enabled budget 0", map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": float64(0)}}, false},
		{"thinking adaptive", map[string]any{"thinking": map[string]any{"type": "adaptive"}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anthropicIsReasoning(tt.req)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpenaiIsReasoning(t *testing.T) {
	tests := []struct {
		effort string
		want   bool
	}{
		{"", true},
		{"none", false},
		{"low", true},
		{"high", true},
	}

	for _, tt := range tests {
		got := openaiIsReasoning(tt.effort)
		if got != tt.want {
			t.Errorf("openaiIsReasoning(%q): got %v, want %v", tt.effort, got, tt.want)
		}
	}
}

func TestBuildLingmaBody_Params(t *testing.T) {
	messages := []map[string]any{{"role": "user", "content": "hello"}}
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "test"}}}
	rawJSON := []byte(`{"model":"test"}`)

	// Test with isReasoning=false and toolChoice
	body := BuildLingmaBody(messages, tools, "model1", map[string]any{"temperature": 0.5, "max_tokens": 1000}, rawJSON, false, "auto")

	mc, ok := body["model_config"].(map[string]any)
	if !ok {
		t.Fatal("model_config not found")
	}
	if mc["is_reasoning"] != false {
		t.Errorf("is_reasoning: got %v, want false", mc["is_reasoning"])
	}

	if body["tool_choice"] != "auto" {
		t.Errorf("tool_choice: got %v, want 'auto'", body["tool_choice"])
	}

	// session_id should be deterministic
	sid, ok := body["session_id"].(string)
	if !ok || len(sid) != 32 {
		t.Errorf("session_id: got %q (len=%d), want 32 hex chars", sid, len(sid))
	}

	// Test with nil rawJSON (should use random UUID)
	body2 := BuildLingmaBody(messages, nil, "model1", nil, nil, true, nil)
	sid2, ok := body2["session_id"].(string)
	if !ok || len(sid2) != 36 { // UUID format
		t.Errorf("session_id (nil raw): got %q, want UUID format", sid2)
	}
	if body2["tool_choice"] != nil {
		t.Errorf("tool_choice should be nil when not provided, got %v", body2["tool_choice"])
	}
}

func TestReadSSE_DoneSafetyInjection(t *testing.T) {
	session := &auth.Session{CosyKey: "test-key", UID: "test-uid"}
	client := NewLingmaClient(session)

	// Simulate a stream that ends without [DONE]
	mockResp := `data: {"choices":[{"delta":{"content":"hi"}}]}

`
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

	// Should have content event + injected done event
	foundDone := false
	for _, e := range events {
		if e.Type == "done" {
			foundDone = true
		}
	}
	if !foundDone {
		t.Error("expected injected [DONE] event when stream ends without one")
	}
}

func TestStreamState_Independence(t *testing.T) {
	// Verify that two streamStates are independent
	s1 := &streamState{}
	s2 := &streamState{}

	// s1 processes a thought tag that spans chunks
	s1.splitThoughtTags("before<thought>partial")
	if !s1.inThought {
		t.Error("s1 should be inThought after open tag")
	}

	// s2 should be unaffected
	if s2.inThought {
		t.Error("s2 should not be affected by s1")
	}

	// s1 continues with the next chunk
	events := s1.splitThoughtTags("end</thought>after")
	if s1.inThought {
		t.Error("s1 should no longer be inThought after close tag")
	}

	var contents, reasonings []string
	for _, ev := range events {
		if ev.Content != "" {
			contents = append(contents, ev.Content)
		}
		if ev.ReasoningContent != "" {
			reasonings = append(reasonings, ev.ReasoningContent)
		}
	}
	if len(contents) != 1 || contents[0] != "after" {
		t.Errorf("expected content [after], got %v", contents)
	}
	if len(reasonings) != 1 || reasonings[0] != "end" {
		t.Errorf("expected reasoning [end], got %v", reasonings)
	}
}
