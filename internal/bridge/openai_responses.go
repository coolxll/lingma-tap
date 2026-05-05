package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
		params["max_tokens"] = *req.MaxTokens
	}

	body := BuildLingmaBody(messages, req.Tools, modelKey, params)

	respID := "resp_" + newUUID()[:24]

	if req.Stream {
		h.streamResponses(r.Context(), w, respID, modelKey, body)
	} else {
		h.nonStreamResponses(r.Context(), w, respID, modelKey, body)
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
			if m, ok := item.(map[string]any); ok {
				msg := map[string]any{"role": "user"}
				if content, ok := m["content"]; ok {
					msg["content"] = content
				} else if text, ok := m["text"]; ok {
					msg["content"] = text
				}
				messages = append(messages, msg)
			}
		}
		return messages
	}
	return nil
}

func (h *BridgeHandler) streamResponses(ctx context.Context, w http.ResponseWriter, respID, modelKey string, body map[string]any) {
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

	blockIndex := 0
	blockStarted := false

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		switch event.Type {
		case "data":
			if event.Content != "" {
				if !blockStarted {
					// Start a text output block
					writeSSE(w, "", map[string]any{
						"type":  "response.output_item.added",
						"index": blockIndex,
						"item": map[string]any{
							"id":     "msg_" + newUUID()[:24],
							"type":   "message",
							"role":   "assistant",
							"status": "in_progress",
							"content": []map[string]any{{
								"type": "output_text",
								"text": "",
							}},
						},
					})
					writeSSE(w, "", map[string]any{
						"type":       "response.content_part.added",
						"item_index": blockIndex,
						"part":       map[string]any{"type": "output_text", "text": ""},
					})
					blockStarted = true
				}

				// Stream text delta
				writeSSE(w, "", map[string]any{
					"type":       "response.output_text.delta",
					"item_index": blockIndex,
					"delta":      event.Content,
				})
				if canFlush {
					flusher.Flush()
				}
			}

			if event.FinishReason != "" {
				// Finish the text block
				if blockStarted {
					writeSSE(w, "", map[string]any{
						"type":       "response.content_part.done",
						"item_index": blockIndex,
						"part":       map[string]any{"type": "output_text", "text": ""},
					})
					writeSSE(w, "", map[string]any{
						"type":       "response.output_item.done",
						"item_index": blockIndex,
					})
				}
			}

		case "finish":
			// Send response.completed
			writeSSE(w, "", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":     respID,
					"status": "completed",
				},
			})
			if canFlush {
				flusher.Flush()
			}
		}
		return nil
	})

	if err != nil {
		writeSSE(w, "", map[string]any{
			"type":  "response.failed",
			"error": map[string]any{"message": err.Error()},
		})
		if canFlush {
			flusher.Flush()
		}
	}
}

func (h *BridgeHandler) nonStreamResponses(ctx context.Context, w http.ResponseWriter, respID, modelKey string, body map[string]any) {
	var fullContent strings.Builder

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		if event.Type == "data" && event.Content != "" {
			fullContent.WriteString(event.Content)
		}
		return nil
	})

	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := map[string]any{
		"id":     respID,
		"object": "response",
		"status": "completed",
		"model":  modelKey,
		"output": []map[string]any{{
			"id":     "msg_" + newUUID()[:24],
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{{
				"type": "output_text",
				"text": fullContent.String(),
			}},
		}},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
