package proto

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/tmaxmax/go-sse"
)

// ParseSSEEvents parses a text/event-stream response body into structured SSE events.
// It handles the double-JSON encoding used by Lingma:
//
//	data:{"headers":{...},"body":"<inner JSON string>","statusCodeValue":200,"statusCode":"OK"}
//
// The inner "body" field is a JSON string that gets parsed separately.
func ParseSSEEvents(body string) []SSEEvent {
	var events []SSEEvent
	r := strings.NewReader(body)

	for ev, err := range sse.Read(r, nil) {
		if err != nil {
			break
		}

		if len(ev.Data) == 0 {
			continue
		}

		events = append(events, buildSSEEvents(ev.Data, ev.Type)...)
	}

	if len(events) == 0 {
		events = parseJSONStreamEvents(body)
	}

	return events
}

func buildSSEEvents(data, eventType string) []SSEEvent {
	if strings.Contains(data, "\n") {
		lineEvents := parseJSONLineEventsWithType(data, eventType)
		if len(lineEvents) > 1 {
			return lineEvents
		}
	}
	return []SSEEvent{buildSSEEvent(data, eventType)}
}

func buildSSEEvent(data, eventType string) SSEEvent {
	evt := SSEEvent{
		EventType: eventType,
		Data:      data,
	}

	// Try to parse the outer JSON envelope.
	var envelope struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(data), &envelope); err == nil && envelope.Body != "" {
		// Try to pretty-print the inner body as JSON.
		var inner interface{}
		if err := json.Unmarshal([]byte(envelope.Body), &inner); err == nil {
			if pretty, err := json.MarshalIndent(inner, "", "  "); err == nil {
				evt.Body = string(pretty)
			} else {
				evt.Body = envelope.Body
			}
		} else {
			// Inner body is not JSON (e.g., "[DONE]").
			evt.Body = envelope.Body
		}
	}

	return evt
}

func parseJSONStreamEvents(body string) []SSEEvent {
	var events []SSEEvent
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}

	dec := json.NewDecoder(strings.NewReader(trimmed))
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err != io.EOF {
				return parseJSONLineEvents(trimmed)
			}
			break
		}
		events = append(events, buildSSEEvent(string(raw), ""))
	}

	return events
}

func parseJSONLineEvents(body string) []SSEEvent {
	return parseJSONLineEventsWithType(body, "")
}

func parseJSONLineEventsWithType(body, eventType string) []SSEEvent {
	var events []SSEEvent
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		raw := json.RawMessage(trimmed)
		if !json.Valid(raw) {
			continue
		}
		compact := &bytes.Buffer{}
		if err := json.Compact(compact, raw); err != nil {
			continue
		}
		events = append(events, buildSSEEvent(compact.String(), eventType))
	}
	return events
}

func hasStreamPayload(events []SSEEvent) bool {
	for _, evt := range events {
		if evt.Body == "[DONE]" {
			return true
		}
		if evt.Body != "" && looksLikeAIStreamPayload(evt.Body) {
			return true
		}
		if evt.Data != "" && looksLikeAIStreamPayload(evt.Data) {
			return true
		}
	}
	return false
}

func looksLikeAIStreamPayload(raw string) bool {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return false
	}
	if _, ok := obj["choices"]; ok {
		return true
	}
	if _, ok := obj["output"]; ok {
		return true
	}
	if typ, ok := obj["type"].(string); ok {
		return strings.Contains(typ, "response.") || strings.Contains(typ, "content_block")
	}
	if body, ok := obj["body"]; ok {
		switch v := body.(type) {
		case string:
			if v == "[DONE]" {
				return true
			}
			return looksLikeAIStreamPayload(v)
		case map[string]interface{}:
			_, hasChoices := v["choices"]
			_, hasOutput := v["output"]
			return hasChoices || hasOutput
		}
	}
	return false
}
