package bridge

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
)

// responsesStreamMachine drives SSE emission for the Responses API streaming path.
// It wraps a responsesAggregator and emits standard event: + data: SSE frames.
type responsesStreamMachine struct {
	agg         *responsesAggregator
	writer      http.ResponseWriter
	flusher     http.Flusher
	headersSent bool
}

// newResponsesStreamMachine creates a stream machine for a given response.
func newResponsesStreamMachine(agg *responsesAggregator, w http.ResponseWriter) *responsesStreamMachine {
	flusher, _ := w.(http.Flusher)
	return &responsesStreamMachine{
		agg:     agg,
		writer:  w,
		flusher: flusher,
	}
}

// writeEvent writes a standard SSE frame with event name and JSON data.
func (m *responsesStreamMachine) writeEvent(eventType string, data any) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("Failed to marshal SSE event %s: %v", eventType, err)
		return
	}
	fmt.Fprintf(m.writer, "event: %s\ndata: %s\n\n", eventType, jsonBytes)
	if m.flusher != nil {
		m.flusher.Flush()
	}
}

// emitCreated sends response.created and response.in_progress events.
func (m *responsesStreamMachine) emitCreated() {
	m.writeEvent("response.created", map[string]any{
		"type":            "response.created",
		"sequence_number": m.agg.nextSequence(),
		"response":        m.agg.newResponse("in_progress", nil),
	})
	m.writeEvent("response.in_progress", map[string]any{
		"type":            "response.in_progress",
		"sequence_number": m.agg.nextSequence(),
		"response":        m.agg.newResponse("in_progress", nil),
	})
}

// onDataEvent processes an upstream SSE data event and emits corresponding Responses events.
func (m *responsesStreamMachine) onDataEvent(event SSEEvent) {
	// Handle reasoning content
	if event.ReasoningContent != "" {
		itemID, itemIndex, isNew := m.agg.addReasoningDelta(event.ReasoningContent)
		if isNew {
			m.writeEvent("response.output_item.added", map[string]any{
				"type":            "response.output_item.added",
				"output_index":    itemIndex,
				"sequence_number": m.agg.nextSequence(),
				"item": map[string]any{
					"type":    "reasoning",
					"id":      itemID,
					"status":  "in_progress",
					"content": []map[string]any{},
				},
			})
		}
		m.writeEvent("response.reasoning_text.delta", map[string]any{
			"type":            "response.reasoning_text.delta",
			"item_id":         itemID,
			"output_index":    itemIndex,
			"content_index":   0,
			"sequence_number": m.agg.nextSequence(),
			"delta":           event.ReasoningContent,
		})
	}

	// Handle text content
	if event.Content != "" {
		itemID, itemIndex, isNew := m.agg.addTextDelta(event.Content)
		if isNew {
			m.writeEvent("response.output_item.added", map[string]any{
				"type":            "response.output_item.added",
				"output_index":    itemIndex,
				"sequence_number": m.agg.nextSequence(),
				"item": map[string]any{
					"id":     itemID,
					"type":   "message",
					"role":   "assistant",
					"status": "in_progress",
					"content": []map[string]any{{
						"type": "output_text",
						"text": "",
					}},
				},
			})
			m.writeEvent("response.content_part.added", map[string]any{
				"type":            "response.content_part.added",
				"item_id":         itemID,
				"output_index":    itemIndex,
				"content_index":   0,
				"sequence_number": m.agg.nextSequence(),
				"part":            map[string]any{"type": "output_text", "text": ""},
			})
		}
		m.writeEvent("response.output_text.delta", map[string]any{
			"type":            "response.output_text.delta",
			"item_id":         itemID,
			"output_index":    itemIndex,
			"content_index":   0,
			"sequence_number": m.agg.nextSequence(),
			"delta":           event.Content,
		})
	}

	// Handle tool calls
	for _, tc := range event.ToolCalls {
		itemID, callID, outputIndex, isNew := m.agg.addToolCall(tc.Index, tc.ID, tc.Name, tc.Arguments)
		if isNew {
			m.writeEvent("response.output_item.added", map[string]any{
				"type":            "response.output_item.added",
				"output_index":    outputIndex,
				"sequence_number": m.agg.nextSequence(),
				"item": map[string]any{
					"type":      "function_call",
					"id":        itemID,
					"call_id":   callID,
					"name":      m.agg.restoreToolName(tc.Name),
					"arguments": "",
					"status":    "in_progress",
				},
			})
		}
		if tc.Arguments != "" {
			m.writeEvent("response.function_call_arguments.delta", map[string]any{
				"type":            "response.function_call_arguments.delta",
				"item_id":         itemID,
				"output_index":    outputIndex,
				"sequence_number": m.agg.nextSequence(),
				"delta":           tc.Arguments,
			})
		}
	}
}

// emitDone closes all active blocks and emits the response.completed event.
// Returns the completed response object.
func (m *responsesStreamMachine) emitDone() *responsesResponse {
	// Collect all finished items to emit closing events in output_index order
	var finishedItems []*responsesOutputItemState

	// Finish active text block
	if m.agg.activeText != nil {
		if item := m.agg.finishTextBlock(); item != nil {
			finishedItems = append(finishedItems, item)
		}
	}

	// Finish active reasoning block
	if m.agg.activeReason != nil {
		if item := m.agg.finishReasoningBlock(); item != nil {
			finishedItems = append(finishedItems, item)
		}
	}

	// Finish all tool calls
	indices := make([]int, 0, len(m.agg.toolCalls))
	for idx := range m.agg.toolCalls {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		if item := m.agg.finishToolCall(idx); item != nil {
			finishedItems = append(finishedItems, item)
		}
	}

	// Sort all finished items by output_index to maintain correct order
	sort.Slice(finishedItems, func(i, j int) bool {
		return finishedItems[i].index < finishedItems[j].index
	})

	// Emit closing events in output_index order
	for _, item := range finishedItems {
		switch item.itemType {
		case "message":
			blockText := item.content[0].Text
			m.writeEvent("response.content_part.done", map[string]any{
				"type":            "response.content_part.done",
				"item_id":         item.id,
				"output_index":    item.index,
				"content_index":   0,
				"sequence_number": m.agg.nextSequence(),
				"part":            map[string]any{"type": "output_text", "text": blockText},
			})
			m.writeEvent("response.output_text.done", map[string]any{
				"type":            "response.output_text.done",
				"item_id":         item.id,
				"output_index":    item.index,
				"content_index":   0,
				"sequence_number": m.agg.nextSequence(),
				"text":            blockText,
			})
			m.writeEvent("response.output_item.done", map[string]any{
				"type":            "response.output_item.done",
				"output_index":    item.index,
				"sequence_number": m.agg.nextSequence(),
				"item": map[string]any{
					"id":     item.id,
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []map[string]any{{
						"type": "output_text",
						"text": blockText,
					}},
				},
			})

		case "reasoning":
			m.writeEvent("response.reasoning_text.done", map[string]any{
				"type":            "response.reasoning_text.done",
				"item_id":         item.id,
				"output_index":    item.index,
				"content_index":   0,
				"sequence_number": m.agg.nextSequence(),
				"text":            item.content[0].Text,
			})
			m.writeEvent("response.output_item.done", map[string]any{
				"type":            "response.output_item.done",
				"output_index":    item.index,
				"sequence_number": m.agg.nextSequence(),
				"item": map[string]any{
					"type":   "reasoning",
					"id":     item.id,
					"status": "completed",
					"content": []map[string]any{
						{"type": "reasoning_text", "text": item.content[0].Text},
					},
				},
			})

		case "function_call":
			m.writeEvent("response.function_call_arguments.done", map[string]any{
				"type":            "response.function_call_arguments.done",
				"item_id":         item.id,
				"output_index":    item.index,
				"sequence_number": m.agg.nextSequence(),
				"name":            item.name,
				"arguments":       item.arguments,
			})
			m.writeEvent("response.output_item.done", map[string]any{
				"type":            "response.output_item.done",
				"output_index":    item.index,
				"sequence_number": m.agg.nextSequence(),
				"item": map[string]any{
					"type":      "function_call",
					"id":        item.id,
					"call_id":   item.callID,
					"name":      item.name,
					"arguments": item.arguments,
					"status":    "completed",
				},
			})
		}
	}

	// Build and emit completed response (items already added by finish methods above)
	resp := m.agg.buildResponse("completed")
	m.writeEvent("response.completed", map[string]any{
		"type":            "response.completed",
		"sequence_number": m.agg.nextSequence(),
		"response":        resp,
	})
	return resp
}

// emitFailed emits a response.failed event with partial output if streaming has started.
func (m *responsesStreamMachine) emitFailed(errMsg string) {
	// Close any active blocks first (partial output)
	if m.agg.activeText != nil {
		m.agg.finishTextBlock()
	}
	if m.agg.activeReason != nil {
		m.agg.finishReasoningBlock()
	}
	for idx := range m.agg.toolCalls {
		m.agg.finishToolCall(idx)
	}

	resp := m.agg.buildResponse("failed")
	resp.Error = &responsesError{
		Type:    "server_error",
		Message: errMsg,
	}

	m.writeEvent("response.failed", map[string]any{
		"type":            "response.failed",
		"sequence_number": m.agg.nextSequence(),
		"response":        resp,
	})
}
