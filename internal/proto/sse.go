package proto

import (
	"encoding/json"
	"strings"
)

// ParseSSEEvents parses a text/event-stream response body into structured SSE events.
// It handles the double-JSON encoding used by Lingma:
//   data:{"headers":{...},"body":"<inner JSON string>","statusCodeValue":200,"statusCode":"OK"}
//
// The inner "body" field is a JSON string that gets parsed separately.
func ParseSSEEvents(body string) []SSEEvent {
	var events []SSEEvent
	chunks := strings.Split(body, "\n\n")

	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		var eventType, data string
		lines := strings.Split(chunk, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}

		if data == "" {
			continue
		}

		evt := SSEEvent{
			EventType: eventType,
			Data:      data,
		}

		// Try to parse the outer JSON envelope
		var envelope struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err == nil && envelope.Body != "" {
			// Try to pretty-print the inner body as JSON
			var inner interface{}
			if err := json.Unmarshal([]byte(envelope.Body), &inner); err == nil {
				if pretty, err := json.MarshalIndent(inner, "", "  "); err == nil {
					evt.Body = string(pretty)
				} else {
					evt.Body = envelope.Body
				}
			} else {
				// Inner body is not JSON (e.g., "[DONE]")
				evt.Body = envelope.Body
			}
		}

		events = append(events, evt)
	}

	return events
}
