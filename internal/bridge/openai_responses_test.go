package bridge

import (
	"testing"
)

func TestResponsesInputToMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected func(t *testing.T, msgs []map[string]any)
	}{
		{
			name:  "string input converts to user message",
			input: "Hello, world!",
			expected: func(t *testing.T, msgs []map[string]any) {
				if len(msgs) != 1 {
					t.Fatalf("expected 1 message, got %d", len(msgs))
				}
				if msgs[0]["role"] != "user" {
					t.Errorf("expected role user, got %v", msgs[0]["role"])
				}
			},
		},
		{
			name: "function_call input converts to assistant with tool_calls",
			input: []any{
				map[string]any{
					"type":      "function_call",
					"call_id":   "call_123",
					"name":      "get_weather",
					"arguments": `{"location": "Beijing"}`,
				},
			},
			expected: func(t *testing.T, msgs []map[string]any) {
				if len(msgs) != 1 {
					t.Fatalf("expected 1 message, got %d", len(msgs))
				}
				if msgs[0]["role"] != "assistant" {
					t.Errorf("expected role assistant, got %v", msgs[0]["role"])
				}
				toolCalls, ok := msgs[0]["tool_calls"].([]map[string]any)
				if !ok || len(toolCalls) != 1 {
					t.Fatalf("expected 1 tool_call, got %v", msgs[0]["tool_calls"])
				}
				if toolCalls[0]["id"] != "call_123" {
					t.Errorf("expected call_id call_123, got %v", toolCalls[0]["id"])
				}
			},
		},
		{
			name: "function_call_output converts to tool message",
			input: []any{
				map[string]any{
					"type":   "function_call_output",
					"call_id": "call_123",
					"output":  "Sunny, 25°C",
				},
			},
			expected: func(t *testing.T, msgs []map[string]any) {
				if len(msgs) != 1 {
					t.Fatalf("expected 1 message, got %d", len(msgs))
				}
				if msgs[0]["role"] != "tool" {
					t.Errorf("expected role tool, got %v", msgs[0]["role"])
				}
			},
		},
		{
			name: "mixed input with text and tool calls",
			input: []any{
				map[string]any{
					"type":    "message",
					"role":    "user",
					"content": "What's the weather?",
				},
				map[string]any{
					"type":      "function_call",
					"call_id":   "call_456",
					"name":      "get_weather",
					"arguments": `{"location": "Shanghai"}`,
				},
			},
			expected: func(t *testing.T, msgs []map[string]any) {
				if len(msgs) != 2 {
					t.Fatalf("expected 2 messages, got %d", len(msgs))
				}
			},
		},
		{
			name:  "empty string returns nil",
			input: "",
			expected: func(t *testing.T, msgs []map[string]any) {
				if msgs != nil {
					t.Errorf("expected nil, got %v", msgs)
				}
			},
		},
		{
			name:  "nil input returns nil",
			input: nil,
			expected: func(t *testing.T, msgs []map[string]any) {
				if msgs != nil {
					t.Errorf("expected nil, got %v", msgs)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := responsesInputToMessages(tt.input)
			if tt.expected != nil {
				tt.expected(t, result)
			}
		})
	}
}
