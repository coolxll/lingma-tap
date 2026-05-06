package proto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSSEEvents(t *testing.T) {
	// Sample SSE stream with Lingma's double-JSON envelope and some other events
	input := `data: {"body": "{\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}"}

data: {"body": "{\"choices\":[{\"delta\":{\"content\":\" world\"}}]}"}

data: {"body": "[DONE]"}

event: ping
data: {"time": "123456"}
`

	events := ParseSSEEvents(input)

	if len(events) != 4 {
		t.Fatalf("Expected 4 events, got %d", len(events))
	}

	// First event
	if events[0].Data == "" {
		t.Errorf("Event 0 data is empty")
	}
	if !strings.Contains(events[0].Body, "Hello") {
		t.Errorf("Event 0 body should contain 'Hello', got: %s", events[0].Body)
	}

	// Third event [DONE]
	if !strings.Contains(events[2].Body, "[DONE]") {
		t.Errorf("Event 2 body should contain '[DONE]', got: %s", events[2].Body)
	}

	// Fourth event (named event)
	if events[3].EventType != "ping" {
		t.Errorf("Event 3 type should be 'ping', got: %s", events[3].EventType)
	}
}

func TestParseSSEEvents_Malformed(t *testing.T) {
	input := `data: malformed json
`
	events := ParseSSEEvents(input)
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}
	if events[0].Data != "malformed json" {
		t.Errorf("Expected data 'malformed json', got: %s", events[0].Data)
	}
	if events[0].Body != "" {
		t.Errorf("Expected empty body for malformed JSON, got: %s", events[0].Body)
	}
}

func TestParseSSEEvents_PrettyPrint(t *testing.T) {
	innerJSON := `{"foo":"bar","baz":123}`
	envelope := struct {
		Body string `json:"body"`
	}{
		Body: innerJSON,
	}
	envelopeJSON, _ := json.Marshal(envelope)
	input := "data: " + string(envelopeJSON) + "\n\n"

	events := ParseSSEEvents(input)
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	// Check if body is pretty-printed (contains newlines/indentation)
	if !strings.Contains(events[0].Body, "\n") || !strings.Contains(events[0].Body, "  ") {
		t.Errorf("Expected pretty-printed body, got: %q", events[0].Body)
	}
}
