package bridge

import (
	"strings"
	"testing"
)

func TestSanitizeAnthropicRequest(t *testing.T) {
	tests := []struct {
		name string
		input map[string]any
		check func(t *testing.T, result map[string]any)
	}{
		{
			name: "strips thinking blocks from messages",
			input: map[string]any{
				"messages": []any{
					map[string]any{
						"role": "assistant",
						"content": []any{
							map[string]any{"type": "thinking", "thinking": "let me think..."},
							map[string]any{"type": "text", "text": "answer"},
						},
					},
				},
			},
			check: func(t *testing.T, result map[string]any) {
				msgs := result["messages"].([]any)
				msg := msgs[0].(map[string]any)
				content := msg["content"].([]any)
				if len(content) != 1 {
					t.Errorf("expected 1 content block after stripping thinking, got %d", len(content))
				}
				block := content[0].(map[string]any)
				if block["type"] != "text" {
					t.Errorf("expected text block, got %v", block["type"])
				}
			},
		},
		{
			name: "strips signature from tool_use blocks",
			input: map[string]any{
				"messages": []any{
					map[string]any{
						"role": "assistant",
						"content": []any{
							map[string]any{"type": "tool_use", "id": "tu1", "name": "get_weather", "signature": "abc123"},
						},
					},
				},
			},
			check: func(t *testing.T, result map[string]any) {
				msgs := result["messages"].([]any)
				msg := msgs[0].(map[string]any)
				content := msg["content"].([]any)
				block := content[0].(map[string]any)
				if _, ok := block["signature"]; ok {
					t.Errorf("expected signature to be stripped from tool_use block")
				}
			},
		},
		{
			name: "strips billing header from system string",
			input: map[string]any{
				"system": "x-anthropic-billing-header: cc_version=2.1.126; cc_entrypoint=cli\nYou are a helpful assistant.",
			},
			check: func(t *testing.T, result map[string]any) {
				sys := result["system"].(string)
				if strings.Contains(sys, "x-anthropic-billing-header") {
					t.Errorf("expected billing header to be stripped, got: %s", sys)
				}
				if !strings.Contains(sys, "You are a helpful assistant") {
					t.Errorf("expected system content to be preserved, got: %s", sys)
				}
			},
		},
		{
			name: "caps budget_tokens at 2048",
			input: map[string]any{
				"thinking": map[string]any{
					"type":         "enabled",
					"budget_tokens": 10000.0,
				},
			},
			check: func(t *testing.T, result map[string]any) {
				thinking := result["thinking"].(map[string]any)
				if thinking["budget_tokens"] != float64(2048) {
					t.Errorf("expected budget_tokens to be capped at 2048, got %v", thinking["budget_tokens"])
				}
			},
		},
		{
			name: "preserves normal messages",
			input: map[string]any{
				"messages": []any{
					map[string]any{
						"role":    "user",
						"content": "Hello",
					},
				},
			},
			check: func(t *testing.T, result map[string]any) {
				msgs := result["messages"].([]any)
				if len(msgs) != 1 {
					t.Errorf("expected 1 message, got %d", len(msgs))
				}
			},
		},
		{
			name: "strips billing header from string message",
			input: map[string]any{
				"messages": []any{
					map[string]any{
						"role":    "user",
						"content": "x-anthropic-billing-header: test\nHello",
					},
				},
			},
			check: func(t *testing.T, result map[string]any) {
				msgs := result["messages"].([]any)
				msg := msgs[0].(map[string]any)
				if msg["content"] != "Hello" {
					t.Errorf("expected content 'Hello', got %v", msg["content"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeAnthropicRequest(tt.input)
			tt.check(t, result)
		})
	}
}

func TestStripBillingHeader(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "x-anthropic-billing-header: cc_version=2.1.126\nYou are helpful.",
			expected: "You are helpful.",
		},
		{
			input:    "x-anthropic-billing-header: cc_version=2.1.126\nFirst line\nSecond line",
			expected: "First line\nSecond line",
		},
		{
			input:    "No billing header here",
			expected: "No billing header here",
		},
		{
			input:    "x-anthropic-billing-header: a\nx-anthropic-billing-header: b\nActual content",
			expected: "Actual content",
		},
	}

	for _, tt := range tests {
		result := stripBillingHeader(tt.input)
		if strings.TrimSpace(result) != strings.TrimSpace(tt.expected) {
			t.Errorf("stripBillingHeader(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestAnthropicToOpenAIMessages(t *testing.T) {
	tests := []struct {
		name     string
		system   any
		messages []map[string]any
		check    func(t *testing.T, result []map[string]any)
	}{
		{
			name:   "converts string system",
			system: "You are helpful.",
			messages: []map[string]any{
				{"role": "user", "content": "Hello"},
			},
			check: func(t *testing.T, result []map[string]any) {
				if len(result) < 2 {
					t.Fatalf("expected at least 2 messages (system + user), got %d", len(result))
				}
				if result[0]["role"] != "system" {
					t.Errorf("expected first message to be system, got %v", result[0]["role"])
				}
			},
		},
		{
			name: "converts assistant message with tool_use",
			messages: []map[string]any{
				{
					"role": "assistant",
					"content": []any{
						map[string]any{"type": "text", "text": "Let me check."},
						map[string]any{
							"type": "tool_use",
							"id":   "tu_123",
							"name": "get_weather",
							"input": map[string]any{"location": "Beijing"},
						},
					},
				},
			},
			check: func(t *testing.T, result []map[string]any) {
				if len(result) != 1 {
					t.Fatalf("expected 1 message, got %d", len(result))
				}
				msg := result[0]
				if msg["role"] != "assistant" {
					t.Errorf("expected assistant role, got %v", msg["role"])
				}
				if msg["content"] == nil {
					t.Error("expected content to be preserved")
				}
				toolCalls, ok := msg["tool_calls"].([]map[string]any)
				if !ok || len(toolCalls) != 1 {
					t.Fatalf("expected 1 tool_call, got %v", msg["tool_calls"])
				}
				if toolCalls[0]["id"] != "tu_123" {
					t.Errorf("expected tool_call id tu_123, got %v", toolCalls[0]["id"])
				}
			},
		},
		{
			name: "converts user message with tool_result",
			messages: []map[string]any{
				{
					"role": "user",
					"content": []any{
						map[string]any{
							"type":       "tool_result",
							"tool_use_id": "tu_123",
							"content":    "Sunny, 25°C",
						},
					},
				},
			},
			check: func(t *testing.T, result []map[string]any) {
				found := false
				for _, m := range result {
					if m["role"] == "tool" {
						found = true
						if m["tool_call_id"] != "tu_123" {
							t.Errorf("expected tool_call_id tu_123, got %v", m["tool_call_id"])
						}
					}
				}
				if !found {
					t.Error("expected a tool message")
				}
			},
		},
		{
			name: "handles simple string content",
			messages: []map[string]any{
				{"role": "user", "content": "Hello"},
				{"role": "assistant", "content": "Hi there!"},
			},
			check: func(t *testing.T, result []map[string]any) {
				if len(result) != 2 {
					t.Fatalf("expected 2 messages, got %d", len(result))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := anthropicToOpenAIMessages(tt.system, tt.messages)
			tt.check(t, result)
		})
	}
}

func TestMapFinishReason(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
		{"content_filter", "stop_sequence"},
		{"unknown", "end_turn"},
		{"", "end_turn"},
	}

	for _, tt := range tests {
		result := mapFinishReason(tt.input)
		if result != tt.expected {
			t.Errorf("mapFinishReason(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
