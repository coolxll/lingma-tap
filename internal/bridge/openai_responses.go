package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
		Model           string           `json:"model"`
		Input           any              `json:"input"` // string or array of content items
		Tools           []map[string]any `json:"tools"`
		ToolChoice      any              `json:"tool_choice"`
		Stream          bool             `json:"stream"`
		Temperature     *float64         `json:"temperature"`
		MaxTokens       *int             `json:"max_output_tokens"`
		ReasoningEffort string           `json:"reasoning_effort"`
	}

	// Read raw body for deterministic session_id
	rawBody, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(rawBody))

	if err := json.Unmarshal(rawBody, &req); err != nil {
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

	isReasoning := openaiIsReasoning(req.ReasoningEffort)

	body := BuildLingmaBody(messages, req.Tools, modelKey, params, rawBody, isReasoning, req.ToolChoice)
	profile := inspectLingmaRequest(body, modelKey)
	fallback := h.applyThinkingFallback("openai_responses", modelKey, rawBody, body, profile)
	if !fallback.Applied {
		h.warnLargeThinkingRequest(modelKey, "openai_responses", profile)
	}

	respID := "resp_" + newUUID()[:24]

	// Initialize Gateway Log
	gLog := &proto.GatewayLog{
		Ts:          time.Now().Format(time.RFC3339Nano),
		Session:     respID,
		Model:       modelKey,
		Method:      r.Method,
		Path:        r.URL.Path,
		RequestBody: h.captureRequestBody(body),
		IsSSE:       req.Stream,
	}
	startTime := time.Now()
	h.recorder(gLog)

	if req.Stream {
		h.streamResponses(r.Context(), w, respID, modelKey, body, gLog, startTime, profile, fallback)
	} else {
		h.nonStreamResponses(r.Context(), w, respID, modelKey, body, gLog, startTime, profile, fallback)
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
					"role":    "assistant",
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

func (h *BridgeHandler) streamResponses(ctx context.Context, w http.ResponseWriter, respID, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time, profile lingmaRequestProfile, fallback lingmaThinkingFallbackDecision) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if fallback.Applied {
		w.Header().Set(lingmaThinkingFallbackHeaderName, lingmaThinkingFallbackHeaderValue)
	}
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
	reasoningBlockStarted := false
	reasoningBlockIndex := -1
	reasoningID := ""
	toolCalls := make(map[int]*toolCallState)
	outputItemIndex := 0 // Monotonic counter for output item indices
	var usage *Usage
	var fullContent strings.Builder
	var fullReasoning strings.Builder
	recordPayloads := h.shouldRecordPayloads()

	upstreamErrored := false
	sawUpstreamEvent := false

	// TTFB self-measurement
	var firstTokenTime time.Time
	firstTokenRecorded := false

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		sawUpstreamEvent = true
		switch event.Type {
		case "data":
			// Record first token time for TTFB self-measurement
			if !firstTokenRecorded && (event.Content != "" || event.ReasoningContent != "") {
				firstTokenTime = time.Now()
				firstTokenRecorded = true
			}

			if event.HasError {
				upstreamErrored = true
				errMsg := orDefault(event.ErrorMsg, "unknown upstream error")
				errType := orDefault(event.ErrorType, "api_error")
				writeSSE(w, "", map[string]any{
					"type": "response.failed",
					"response": map[string]any{
						"id":     respID,
						"status": "failed",
						"model":  modelKey,
						"error": map[string]any{
							"type":    errType,
							"message": errMsg,
						},
					},
				})
				if canFlush {
					flusher.Flush()
				}
				gLog.Error = errMsg
				gLog.Status = http.StatusBadGateway
				gLog.Latency = time.Since(startTime).Milliseconds()
				h.recorder(gLog)
				return nil
			}
			if event.Usage != nil {
				usage = event.Usage
				applyUsageToGatewayLog(gLog, usage)
			}
			// Handle reasoning content
			if event.ReasoningContent != "" {
				fullReasoning.WriteString(event.ReasoningContent)
				if !reasoningBlockStarted {
					reasoningID = "reason_" + newUUID()[:24]
					reasoningBlockIndex = outputItemIndex
					outputItemIndex++
					writeSSE(w, "", map[string]any{
						"type":  "response.output_item.added",
						"index": reasoningBlockIndex,
						"item": map[string]any{
							"type":    "reasoning",
							"id":      reasoningID,
							"status":  "in_progress",
							"content": []map[string]any{},
						},
					})
					reasoningBlockStarted = true
				}
				writeSSE(w, "", map[string]any{
					"type":    "response.reasoning_text.delta",
					"item_id": reasoningID,
					"delta":   event.ReasoningContent,
				})
				if canFlush {
					flusher.Flush()
				}
			}
			// Handle text content
			if event.Content != "" {
				if recordPayloads {
					fullContent.WriteString(event.Content)
				}
				if !textBlockStarted {
					textBlockIndex = outputItemIndex
					outputItemIndex++
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
					state = &toolCallState{
						id:          id,
						name:        tc.Name,
						outputIndex: outputItemIndex,
					}
					outputItemIndex++
					state.args.WriteString(tc.Arguments)
					toolCalls[tc.Index] = state

					writeSSE(w, "", map[string]any{
						"type":  "response.output_item.added",
						"index": state.outputIndex,
						"item": map[string]any{
							"type":      "function_call",
							"id":        state.id,
							"name":      state.name,
							"arguments": state.args.String(),
							"status":    "in_progress",
						},
					})
				} else {
					if tc.ID != "" {
						state.id = tc.ID
					}
					if tc.Name != "" {
						state.name = tc.Name
					}
					state.args.WriteString(tc.Arguments)
				}

				// Emit argument delta
				if tc.Arguments != "" {
					writeSSE(w, "", map[string]any{
						"type":    "response.function_call_arguments.delta",
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
			if upstreamErrored {
				return nil
			}
			applyFinishEvent(gLog, event)
			if event.Usage != nil {
				usage = event.Usage
			}
		case "done":
			if upstreamErrored {
				return nil
			}
			if gLog.TTFT == 0 && firstTokenRecorded {
				gLog.TTFT = firstTokenTime.Sub(startTime).Milliseconds()
			}
			if textBlockStarted {
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
			if reasoningBlockStarted {
				writeSSE(w, "", map[string]any{
					"type":    "response.reasoning_text.done",
					"item_id": reasoningID,
				})
				writeSSE(w, "", map[string]any{
					"type":  "response.output_item.done",
					"index": reasoningBlockIndex,
					"item": map[string]any{
						"type":   "reasoning",
						"id":     reasoningID,
						"status": "completed",
						"content": []map[string]any{
							{"type": "reasoning_text", "text": fullReasoning.String()},
						},
					},
				})
				reasoningBlockStarted = false
			}
			for _, state := range toolCalls {
				writeSSE(w, "", map[string]any{
					"type":  "response.output_item.done",
					"index": state.outputIndex,
					"item": map[string]any{
						"type":      "function_call",
						"id":        state.id,
						"name":      state.name,
						"arguments": state.args.String(),
						"status":    "completed",
					},
				})
			}

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

			gLog.Status = 200
			gLog.Latency = time.Since(startTime).Milliseconds()
			applyUsageToGatewayLog(gLog, usage)

			if recordPayloads {
				output := []map[string]any{}
				if fullReasoning.Len() > 0 {
					output = append(output, map[string]any{
						"type": "reasoning",
						"id":   "reason_" + newUUID()[:24],
						"content": []map[string]any{
							{"type": "reasoning_text", "text": fullReasoning.String()},
						},
					})
				}
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
				h.captureResponseBody(gLog, respSummary)
			}
			h.recorder(gLog)
		}
		return nil
	})

	if err != nil {
		h.rememberThinkingFallback(err, fallback, profile, modelKey, "openai_responses", sawUpstreamEvent)
		if recordContextError(ctx, gLog, startTime, err, h.recorder) {
			return
		}
		message := normalizeLingmaUpstreamError(err)
		gLog.Error = message
		gLog.Status = statusForLingmaUpstreamError(err)
		gLog.Latency = time.Since(startTime).Milliseconds()
		h.recorder(gLog)

		writeSSE(w, "", map[string]any{
			"type":  "response.failed",
			"error": map[string]any{"message": message},
		})
		if canFlush {
			flusher.Flush()
		}
	}
}

func (h *BridgeHandler) nonStreamResponses(ctx context.Context, w http.ResponseWriter, respID, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time, profile lingmaRequestProfile, fallback lingmaThinkingFallbackDecision) {
	var fullContent strings.Builder
	var fullReasoning strings.Builder
	var usage *Usage
	toolCalls := make(map[int]*toolCallState)
	var lastErr *SSEEvent
	var firstTokenTime time.Time
	firstTokenRecorded := false
	sawUpstreamEvent := false

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		sawUpstreamEvent = true
		switch event.Type {
		case "data":
			if event.HasError {
				lastErr = &event
				return nil
			}
			// Record first token time for TTFB self-measurement
			if !firstTokenRecorded && (event.Content != "" || event.ReasoningContent != "") {
				firstTokenTime = time.Now()
				firstTokenRecorded = true
			}
			if event.ReasoningContent != "" {
				fullReasoning.WriteString(event.ReasoningContent)
			}
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
				applyUsageToGatewayLog(gLog, usage)
			}
		case "finish":
			applyFinishEvent(gLog, event)
		}
		return nil
	})

	if err != nil {
		h.rememberThinkingFallback(err, fallback, profile, modelKey, "openai_responses", sawUpstreamEvent)
		if recordStreamError(ctx, gLog, startTime, err, h.recorder) {
			return
		}
		writeOpenAIError(w, statusForLingmaUpstreamError(err), normalizeLingmaUpstreamError(err))
		return
	}

	// Check for upstream error in SSE events
	if lastErr != nil {
		errMsg := orDefault(lastErr.ErrorMsg, "unknown upstream error")
		gLog.Error = errMsg
		gLog.Status = http.StatusBadGateway
		gLog.Latency = time.Since(startTime).Milliseconds()
		h.recorder(gLog)
		writeOpenAIError(w, http.StatusBadGateway, errMsg)
		return
	}

	// Build output array
	output := []map[string]any{}

	// Add reasoning if present
	if fullReasoning.Len() > 0 {
		output = append(output, map[string]any{
			"type": "reasoning",
			"id":   "reason_" + newUUID()[:24],
			"content": []map[string]any{
				{"type": "reasoning_text", "text": fullReasoning.String()},
			},
		})
	}

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
	h.captureResponseBytes(gLog, respBytes)
	applyUsageToGatewayLog(gLog, usage)
	gLog.Latency = time.Since(startTime).Milliseconds()

	// TTFB fallback: use self-measured value if upstream didn't provide one
	if gLog.TTFT == 0 && firstTokenRecorded {
		gLog.TTFT = firstTokenTime.Sub(startTime).Milliseconds()
	}

	h.recorder(gLog)

	if fallback.Applied {
		w.Header().Set(lingmaThinkingFallbackHeaderName, lingmaThinkingFallbackHeaderValue)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}
