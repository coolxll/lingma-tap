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

func TestParseSSEEvents_LingmaJSONLines(t *testing.T) {
	input := `{"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[{\"delta\":{\"content\":\"跟你\"},\"index\":0}],\"model\":\"auto\"}","statusCodeValue":200,"statusCode":"OK"}
{"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[{\"delta\":{\"content\":\"打招呼\"},\"index\":0}],\"model\":\"auto\"}","statusCodeValue":200,"statusCode":"OK"}
{"firstTokenDuration":277,"totalDuration":409,"serverDuration":16}
`

	events := ParseSSEEvents(input)
	if len(events) != 3 {
		t.Fatalf("Expected 3 events, got %d", len(events))
	}
	if !strings.Contains(events[0].Body, "跟你") {
		t.Errorf("Expected first event body to contain extracted inner content, got: %s", events[0].Body)
	}
	if !strings.Contains(events[1].Body, "打招呼") {
		t.Errorf("Expected second event body to contain extracted inner content, got: %s", events[1].Body)
	}
	if events[2].Body != "" {
		t.Errorf("Expected stats metadata to have empty body, got: %s", events[2].Body)
	}
	if !hasStreamPayload(events) {
		t.Errorf("Expected Lingma JSON lines to be recognized as stream payload")
	}
}

func TestParseSSEEvents_ConcatenatedLingmaJSON(t *testing.T) {
	input := `{"body":"{\"choices\":[{\"delta\":{\"content\":\"A\"}}]}"}` +
		`{"body":"{\"choices\":[{\"delta\":{\"content\":\"B\"}}]}"}` +
		`{"firstTokenDuration":277}`

	events := ParseSSEEvents(input)
	if len(events) != 3 {
		t.Fatalf("Expected 3 events, got %d", len(events))
	}
	if !strings.Contains(events[0].Body, "A") || !strings.Contains(events[1].Body, "B") {
		t.Fatalf("Expected concatenated JSON envelopes to be parsed, got: %+v", events)
	}
}

func TestParseSSEEvents_DataLinesWithoutBlankSeparator(t *testing.T) {
	input := `data:{"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[{\"delta\":{\"reasoning_content\":\"User\"},\"index\":0}],\"model\":\"auto\"}","statusCodeValue":200,"statusCode":"OK"}
data:{"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[{\"delta\":{\"content\":\"嗨！\"},\"index\":0}],\"model\":\"auto\"}","statusCodeValue":200,"statusCode":"OK"}
data:{"headers":{"Content-Type":["application/json"]},"body":"{\"choices\":[{\"delta\":{\"content\":\"我随时在线。\"},\"index\":0}],\"model\":\"auto\"}","statusCodeValue":200,"statusCode":"OK"}
`

	events := ParseSSEEvents(input)
	if len(events) != 3 {
		t.Fatalf("Expected 3 events from adjacent data lines, got %d: %+v", len(events), events)
	}
	if !strings.Contains(events[1].Body, "嗨！") || !strings.Contains(events[2].Body, "我随时在线。") {
		t.Fatalf("Expected adjacent data line bodies to be extracted, got: %+v", events)
	}
}
