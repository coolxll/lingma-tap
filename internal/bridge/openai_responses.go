package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

// HandleOpenAIResponses handles POST /v1/responses (OpenAI Responses API)
func (h *BridgeHandler) HandleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"Method not allowed","type":"invalid_request_error"}}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Model       string           `json:"model"`
		Input       any              `json:"input"` // string or array of content items
		Tools       []map[string]any `json:"tools"`
		Stream      bool             `json:"stream"`
		Temperature *float64         `json:"temperature"`
		MaxTokens   *int             `json:"max_output_tokens"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}

	// Convert input to messages
	messages := responsesInputToMessages(req.Input)
	if len(messages) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "input is required")
		return
	}

	modelKey := h.resolveModelKey(r.Context(), req.Model)

	params := map[string]any{}
	if req.Temperature != nil {
		params["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		maxTokens := *req.MaxTokens
		if maxTokens > MaxTokensLimit {
			maxTokens = MaxTokensLimit
		}
		params["max_tokens"] = maxTokens
	}

	body := BuildLingmaBody(messages, req.Tools, modelKey, params)

	respID := "resp_" + newUUID()[:24]

	// Initialize Gateway Log
	gLog := &proto.GatewayLog{
		Ts:          time.Now().Format(time.RFC3339Nano),
		Session:     respID,
		Model:       modelKey,
		Method:      r.Method,
		Path:        r.URL.Path,
		RequestBody: func() string { b, _ := json.Marshal(body); return string(b) }(),
	}
	startTime := time.Now()
	h.recorder(gLog)

	if req.Stream {
		h.streamResponses(r.Context(), w, respID, modelKey, body, gLog, startTime)
	} else {
		h.nonStreamResponses(r.Context(), w, respID, modelKey, body, gLog, startTime)
	}
}

func responsesInputToMessages(input any) []map[string]any {
	switch v := input.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []map[string]any{{"role": "user", "content": v}}
	case []any:
		var messages []map[string]any
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			itemType, _ := m["type"].(string)
			switch itemType {
			case "function_call":
				// Convert function_call to assistant message with tool_calls
				id, _ := m["call_id"].(string)
				name, _ := m["name"].(string)
				args, _ := m["arguments"].(string)
				if id == "" {
					id = "call_" + newUUID()[:24]
				}
				messages = append(messages, map[string]any{
					"role": "assistant",
					"content": nil,
					"tool_calls": []map[string]any{
						{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}},
					},
				})
			case "function_call_output":
				// Convert function_call_output to tool message
				callID, _ := m["call_id"].(string)
				output, _ := m["output"].(string)
				messages = append(messages, map[string]any{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      output,
				})
			default:
				// Handle message and other types, respecting the role
				role := "user"
				if r, ok := m["role"].(string); ok {
					role = r
				}
				msg := map[string]any{"role": role}
				if content, ok := m["content"]; ok {
					msg["content"] = content
				} else if text, ok := m["text"].(string); ok {
					msg["content"] = text
				}
				messages = append(messages, msg)
			}
		}
		return messages
	}
	return nil
}

func (h *BridgeHandler) streamResponses(ctx context.Context, w http.ResponseWriter, respID, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)

	// Send response.created event
	created := map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":     respID,
			"object": "response",
			"status": "in_progress",
			"model":  modelKey,
			"output": []any{},
		},
	}
	writeSSE(w, "", created)
	if canFlush {
		flusher.Flush()
	}

	// Send response.in_progress
	writeSSE(w, "", map[string]any{
		"type": "response.in_progress",
		"response": map[string]any{
			"id":     respID,
			"status": "in_progress",
		},
	})
	if canFlush {
		flusher.Flush()
	}

	// State tracking
	textBlockStarted := false
	textBlockIndex := -1
	toolCalls := make(map[int]*toolCallState)
	toolCallIndices := make(map[string]int) // call_id → output item index
	var usage *Usage
	var fullContent strings.Builder

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		switch event.Type {
		case "data":
			if event.Usage != nil {
				usage = event.Usage
			}
			// Handle text content
			if event.Content != "" {
				fullContent.WriteString(event.Content)
				if !textBlockStarted {
					textBlockIndex = len(toolCallIndices)
					// Start a text output block
					writeSSE(w, "", map[string]any{
						"type":  "response.output_item.added",
						"index": textBlockIndex,
						"item": map[string]any{
							"id":     "msg_" + newUUID()[:24],
							"type":   "message",
							"role":   "assistant",
							"status": "in_progress",
							"content": []map[string]any{{
								"type": "text",
								"text": "",
							}},
						},
					})
					writeSSE(w, "", map[string]any{
						"type":       "response.content_part.added",
						"item_index": textBlockIndex,
						"part":       map[string]any{"type": "text", "text": ""},
					})
					textBlockStarted = true
				}

				// Stream text delta
				writeSSE(w, "", map[string]any{
					"type":       "response.output_text.delta",
					"item_index": textBlockIndex,
					"delta":      event.Content,
				})
				if canFlush {
					flusher.Flush()
				}
			}

			// Handle tool calls
			for _, tc := range event.ToolCalls {
				state, ok := toolCalls[tc.Index]
				if !ok {
					id := tc.ID
					if id == "" {
						id = "call_" + newUUID()[:24]
					}
					state = &toolCallState{id: id}
					toolCalls[tc.Index] = state
				}
				if tc.ID != "" {
					state.id = tc.ID
				}
				if tc.Name != "" {
					state.name = tc.Name
				}
				state.args.WriteString(tc.Arguments)

				// Emit function_call output item
				callIndex := len(toolCallIndices)
				toolCallIndices[state.id] = callIndex

				writeSSE(w, "", map[string]any{
					"type":  "response.output_item.added",
					"index": callIndex,
					"item": map[string]any{
						"type":      "function_call",
						"id":        state.id,
						"name":      state.name,
						"arguments": state.args.String(),
						"status":    "in_progress",
					},
				})

				// Emit argument delta
				if tc.Arguments != "" {
					writeSSE(w, "", map[string]any{
						"type":  "response.function_call_arguments.delta",
						"item_id": state.id,
						"delta":   tc.Arguments,
					})
				}
			}

			// Handle finish reason for text block
			if event.FinishReason != "" && textBlockStarted {
				writeSSE(w, "", map[string]any{
					"type":       "response.content_part.done",
					"item_index": textBlockIndex,
					"part":       map[string]any{"type": "text", "text": ""},
				})
				writeSSE(w, "", map[string]any{
					"type":       "response.output_item.done",
					"item_index": textBlockIndex,
				})
				textBlockStarted = false
			}

		case "finish":
			if event.Usage != nil {
				usage = event.Usage
			}
			// Complete any open function calls
			for _, state := range toolCalls {
				writeSSE(w, "", map[string]any{
					"type":  "response.output_item.done",
					"index": toolCallIndices[state.id],
					"item": map[string]any{
						"type":      "function_call",
						"id":        state.id,
						"name":      state.name,
						"arguments": state.args.String(),
						"status":    "completed",
					},
				})
			}

			// Send response.completed
			writeSSE(w, "", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":     respID,
					"status": "completed",
					"model":  modelKey,
					"usage":  usage,
				},
			})
			if canFlush {
				flusher.Flush()
			}

			// Finalize Log
			gLog.Status = 200
			gLog.Latency = time.Since(startTime).Milliseconds()
			if usage != nil {
				gLog.InputTokens = usage.PromptTokens
				gLog.OutputTokens = usage.CompletionTokens
			}

			// Build output array for log
			output := []map[string]any{}
			if fullContent.Len() > 0 {
				output = append(output, map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "text", "text": fullContent.String()},
					},
				})
			}
			for _, tc := range toolCalls {
				output = append(output, map[string]any{
					"type":      "function_call",
					"id":        tc.id,
					"name":      tc.name,
					"arguments": tc.args.String(),
				})
			}
			respSummary := map[string]any{
				"id":     respID,
				"object": "response",
				"status": "completed",
				"model":  modelKey,
				"output": output,
				"usage":  usage,
			}
			respBytes, _ := json.Marshal(respSummary)
			gLog.ResponseBody = string(respBytes)
			h.recorder(gLog)
		}
		return nil
	})

	if err != nil {
		gLog.Error = err.Error()
		gLog.Status = 500
		h.recorder(gLog)

		writeSSE(w, "", map[string]any{
			"type":  "response.failed",
			"error": map[string]any{"message": err.Error()},
		})
		if canFlush {
			flusher.Flush()
		}
	}
}

func (h *BridgeHandler) nonStreamResponses(ctx context.Context, w http.ResponseWriter, respID, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time) {
	var fullContent strings.Builder
	var usage *Usage
	toolCalls := make(map[int]*toolCallState)

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		if event.Type == "data" {
			if event.Content != "" {
				fullContent.WriteString(event.Content)
			}
			for _, tc := range event.ToolCalls {
				if toolCalls[tc.Index] == nil {
					toolCalls[tc.Index] = &toolCallState{id: tc.ID}
				}
				if tc.ID != "" {
					toolCalls[tc.Index].id = tc.ID
				}
				if tc.Name != "" {
					toolCalls[tc.Index].name = tc.Name
				}
				toolCalls[tc.Index].args.WriteString(tc.Arguments)
			}
			if event.Usage != nil {
				usage = event.Usage
			}
		}
		return nil
	})

	if err != nil {
		gLog.Error = err.Error()
		gLog.Status = 500
		h.recorder(gLog)
		writeOpenAIError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Build output array
	output := []map[string]any{}

	// Add text message if there's text content
	if fullContent.Len() > 0 {
		output = append(output, map[string]any{
			"id":     "msg_" + newUUID()[:24],
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{{
				"type": "text",
				"text": fullContent.String(),
			}},
		})
	}

	// Add function_call items
	for _, tc := range toolCalls {
		if tc.id == "" {
			tc.id = "call_" + newUUID()[:24]
		}
		output = append(output, map[string]any{
			"type":      "function_call",
			"id":        tc.id,
			"name":      tc.name,
			"arguments": tc.args.String(),
			"status":    "completed",
		})
	}

	resp := map[string]any{
		"id":     respID,
		"object": "response",
		"status": "completed",
		"model":  modelKey,
		"output": output,
		"usage":  usage,
	}

	respBytes, _ := json.Marshal(resp)

	// Finalize Log
	gLog.Status = 200
	gLog.ResponseBody = string(respBytes)
	if usage != nil {
		gLog.InputTokens = usage.PromptTokens
		gLog.OutputTokens = usage.CompletionTokens
	}
	gLog.Latency = time.Since(startTime).Milliseconds()
	h.recorder(gLog)

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}
