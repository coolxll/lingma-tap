package bridge

import "testing"

func TestResponsesContextTrimmerUsesExplicitCurrentTurnBoundary(t *testing.T) {
	trimmer := newResponsesContextTrimmer(140)
	messages := []map[string]any{
		{"role": "user", "content": "old request with enough history to trim"},
		{"role": "assistant", "content": "old answer with enough history to trim"},
		{"role": "tool", "tool_call_id": "call_current", "content": "current result"},
	}

	trimmedMessages, trimmed, err := trimmer.trimContextAt(messages, 2)
	if err != nil {
		t.Fatalf("trimContextAt returned error: %v", err)
	}
	if !trimmed {
		t.Fatal("expected old history to be trimmed")
	}
	if len(trimmedMessages) != 1 || trimmedMessages[0]["role"] != "tool" {
		t.Fatalf("trimmed messages = %#v, want only current tool result", trimmedMessages)
	}
}
