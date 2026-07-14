package bridge

import (
	"encoding/json"
	"testing"
)

func makeRawInput(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func TestResponsesBuildMessages_StringInput(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput("Hello, world!"),
	}
	msgs, warnings := responsesBuildMessages(req)
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["role"] != "user" {
		t.Errorf("expected role user, got %v", msgs[0]["role"])
	}
	if msgs[0]["content"] != "Hello, world!" {
		t.Errorf("expected content 'Hello, world!', got %v", msgs[0]["content"])
	}
}

func TestResponsesBuildMessages_EmptyStringReturnsNil(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput(""),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for empty string, got %d", len(msgs))
	}
}

func TestResponsesBuildMessages_NilInputReturnsNil(t *testing.T) {
	req := &responsesRequest{}
	msgs, _ := responsesBuildMessages(req)
	if msgs != nil {
		t.Errorf("expected nil for nil input, got %v", msgs)
	}
}

func TestResponsesBuildMessages_Instructions(t *testing.T) {
	req := &responsesRequest{
		Instructions: "You are a helpful assistant.",
		Input:        makeRawInput("Hi"),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0]["role"] != "system" {
		t.Errorf("expected first message role=system, got %v", msgs[0]["role"])
	}
	if msgs[0]["content"] != "You are a helpful assistant." {
		t.Errorf("expected instructions content, got %v", msgs[0]["content"])
	}
	if msgs[1]["role"] != "user" {
		t.Errorf("expected second message role=user, got %v", msgs[1]["role"])
	}
}

func TestResponsesBuildMessages_FunctionCall(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput([]any{
			map[string]any{
				"type":      "function_call",
				"call_id":   "call_123",
				"name":      "get_weather",
				"arguments": `{"location": "Beijing"}`,
			},
		}),
	}
	msgs, _ := responsesBuildMessages(req)
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
}

func TestResponsesBuildMessages_FunctionCallOutput(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput([]any{
			map[string]any{
				"type":    "function_call_output",
				"call_id": "call_123",
				"output":  "Sunny, 25°C",
			},
		}),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["role"] != "tool" {
		t.Errorf("expected role tool, got %v", msgs[0]["role"])
	}
	if msgs[0]["tool_call_id"] != "call_123" {
		t.Errorf("expected tool_call_id call_123, got %v", msgs[0]["tool_call_id"])
	}
}

func TestResponsesBuildMessages_StructuredFunctionCallOutput(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput([]any{
			map[string]any{
				"type":    "function_call_output",
				"call_id": "call_structured",
				"output":  map[string]any{"ok": true, "count": 2},
			},
		}),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if got := msgs[0]["content"]; got != `{"count":2,"ok":true}` {
		t.Fatalf("structured tool output = %v, want JSON encoding", got)
	}
}

func TestResponsesBuildMessages_MergeConsecutiveFunctionCalls(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput([]any{
			map[string]any{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "func_a",
				"arguments": `{}`,
			},
			map[string]any{
				"type":      "function_call",
				"call_id":   "call_2",
				"name":      "func_b",
				"arguments": `{}`,
			},
			map[string]any{
				"type":      "function_call",
				"call_id":   "call_3",
				"name":      "func_c",
				"arguments": `{}`,
			},
		}),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(msgs))
	}
	toolCalls, ok := msgs[0]["tool_calls"].([]map[string]any)
	if !ok {
		t.Fatalf("expected tool_calls slice, got %T", msgs[0]["tool_calls"])
	}
	if len(toolCalls) != 3 {
		t.Errorf("expected 3 merged tool_calls, got %d", len(toolCalls))
	}
}

func TestResponsesBuildMessages_MixedInput(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput([]any{
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
		}),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0]["role"] != "user" {
		t.Errorf("expected first message role=user, got %v", msgs[0]["role"])
	}
	if msgs[1]["role"] != "assistant" {
		t.Errorf("expected second message role=assistant, got %v", msgs[1]["role"])
	}
}

func TestResponsesBuildMessages_DeveloperToSystem(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput([]any{
			map[string]any{
				"type":    "message",
				"role":    "developer",
				"content": "You are a code reviewer.",
			},
		}),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["role"] != "system" {
		t.Errorf("expected role system (normalized from developer), got %v", msgs[0]["role"])
	}
}

func TestResponsesBuildMessages_InputText(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput([]any{
			map[string]any{
				"type": "input_text",
				"text": "Hello from input_text",
			},
		}),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["role"] != "user" {
		t.Errorf("expected role user, got %v", msgs[0]["role"])
	}
	if msgs[0]["content"] != "Hello from input_text" {
		t.Errorf("expected content 'Hello from input_text', got %v", msgs[0]["content"])
	}
}

func TestResponsesBuildMessages_OutputText(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput([]any{
			map[string]any{
				"type": "output_text",
				"text": "Previous assistant response",
			},
		}),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["role"] != "assistant" {
		t.Errorf("expected role assistant, got %v", msgs[0]["role"])
	}
}

func TestResponsesBuildMessages_InputImage(t *testing.T) {
	req := &responsesRequest{
		Input: makeRawInput([]any{
			map[string]any{
				"type":      "input_image",
				"image_url": "https://example.com/img.png",
				"detail":    "high",
			},
		}),
	}
	msgs, _ := responsesBuildMessages(req)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["role"] != "user" {
		t.Errorf("expected role user, got %v", msgs[0]["role"])
	}
	content, ok := msgs[0]["content"].([]map[string]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected 1 content part, got %v", msgs[0]["content"])
	}
	if content[0]["type"] != "image_url" {
		t.Errorf("expected type image_url, got %v", content[0]["type"])
	}
}

func TestResponsesConvertTools_Standard(t *testing.T) {
	tools := []responsesTool{
		{
			Type:        "function",
			Name:        "get_weather",
			Description: "Get weather info",
			Parameters:  map[string]any{"type": "object"},
		},
	}
	result, warnings := responsesConvertTools(tools)
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0]["type"] != "function" {
		t.Errorf("expected type function, got %v", result[0]["type"])
	}
	fn := result[0]["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("expected name get_weather, got %v", fn["name"])
	}
}

func TestResponsesConvertTools_Namespace(t *testing.T) {
	tools := []responsesTool{
		{
			Type: "function",
			Name: "mcp.server.tool_name",
		},
	}
	result, warnings := responsesConvertTools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	fn := result[0]["function"].(map[string]any)
	if fn["name"] != "mcp__server__tool_name" {
		t.Errorf("expected renamed name, got %v", fn["name"])
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for namespace rename, got %d", len(warnings))
	}
}

func TestResponsesConvertTools_UnsupportedType(t *testing.T) {
	tools := []responsesTool{
		{
			Type: "computer_use",
			Name: "computer",
		},
	}
	result, warnings := responsesConvertTools(tools)
	if len(result) != 0 {
		t.Errorf("expected 0 tools for unsupported type, got %d", len(result))
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(warnings))
	}
}

func TestResponsesConvertTools_MissingName(t *testing.T) {
	tools := []responsesTool{
		{
			Type: "function",
			Name: "",
		},
	}
	result, warnings := responsesConvertTools(tools)
	if len(result) != 0 {
		t.Errorf("expected 0 tools for missing name, got %d", len(result))
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(warnings))
	}
}

func TestResponsesConvertTools_Empty(t *testing.T) {
	result, warnings := responsesConvertTools(nil)
	if result != nil {
		t.Errorf("expected nil for empty tools, got %v", result)
	}
	if warnings != nil {
		t.Errorf("expected nil warnings for empty tools, got %v", warnings)
	}
}
