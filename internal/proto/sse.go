package proto

import (
	"encoding/json"
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

		evt := SSEEvent{
			EventType: ev.Type,
			Data:      ev.Data,
		}

		// Try to parse the outer JSON envelope
		var envelope struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &envelope); err == nil && envelope.Body != "" {
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
