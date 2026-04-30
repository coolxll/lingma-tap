package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// HandleAnthropicMessages handles POST /v1/messages (Anthropic Messages API)
func (h *BridgeHandler) HandleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Method not allowed"}}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Model       string           `json:"model"`
		System      any              `json:"system"` // string or array of content blocks
		Messages    []map[string]any `json:"messages"`
		Tools       []map[string]any `json:"tools"`
		Stream      bool             `json:"stream"`
		Temperature *float64         `json:"temperature"`
		MaxTokens   int              `json:"max_tokens"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if len(req.Messages) == 0 {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "messages is required")
		return
	}

	if req.MaxTokens == 0 {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "max_tokens is required")
		return
	}

	modelKey := mapAnthropicModelToLingma(req.Model)

	// Convert Anthropic messages to OpenAI format
	messages := anthropicToOpenAIMessages(req.System, req.Messages)

	// Convert Anthropic tools to OpenAI format
	var tools []map[string]any
	if len(req.Tools) > 0 {
		tools = anthropicToOpenAITools(req.Tools)
	}

	params := map[string]any{
		"max_tokens": req.MaxTokens,
	}
	if req.Temperature != nil {
		params["temperature"] = *req.Temperature
	}

	body := BuildLingmaBody(messages, tools, modelKey, params)

	msgID := "msg_" + newUUID()[:24]

	if req.Stream {
		h.streamAnthropic(r.Context(), w, msgID, modelKey, body, req.MaxTokens)
	} else {
		h.nonStreamAnthropic(r.Context(), w, msgID, modelKey, body, req.MaxTokens)
	}
}

func anthropicToOpenAIMessages(system any, messages []map[string]any) []map[string]any {
	var result []map[string]any

	// Add system message if present
	if system != nil {
		var content string
		switch v := system.(type) {
		case string:
			content = v
		case []any:
			// Array of content blocks
			var parts []string
			for _, block := range v {
				if m, ok := block.(map[string]any); ok {
					if text, ok := m["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
			content = strings.Join(parts, "\n")
		}
		if content != "" {
			result = append(result, map[string]any{"role": "system", "content": content})
		}
	}

	// Convert messages
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content := msg["content"]

		openaiMsg := map[string]any{"role": role}

		switch v := content.(type) {
		case string:
			openaiMsg["content"] = v
			result = append(result, openaiMsg)
		case []any:
			// Array of content blocks
			var textParts []string
			var toolResults []map[string]any
			for _, block := range v {
				if m, ok := block.(map[string]any); ok {
					blockType, _ := m["type"].(string)
					switch blockType {
					case "text":
						if text, ok := m["text"].(string); ok {
							textParts = append(textParts, text)
						}
					case "tool_use":
						// Anthropic tool use → OpenAI assistant tool_calls
						// This will be handled separately
					case "tool_result":
						toolUseID, _ := m["tool_use_id"].(string)
						var resultContent string
						if rc, ok := m["content"].(string); ok {
							resultContent = rc
						} else if rcBlocks, ok := m["content"].([]any); ok {
							for _, rb := range rcBlocks {
								if rm, ok := rb.(map[string]any); ok {
									if text, ok := rm["text"].(string); ok {
										resultContent += text
									}
								}
							}
						}
						toolResults = append(toolResults, map[string]any{
							"role":         "tool",
							"tool_call_id": toolUseID,
							"content":      resultContent,
						})
					}
				}
			}

			if len(toolResults) > 0 {
				// Add text part first if any
				if len(textParts) > 0 {
					openaiMsg["content"] = strings.Join(textParts, "\n")
					result = append(result, openaiMsg)
				}
				// Add tool results as separate messages
				result = append(result, toolResults...)
			} else if len(textParts) > 0 {
				openaiMsg["content"] = strings.Join(textParts, "\n")
				result = append(result, openaiMsg)
			}
		default:
			openaiMsg["content"] = fmt.Sprintf("%v", content)
			result = append(result, openaiMsg)
		}
	}

	return result
}

func anthropicToOpenAITools(tools []map[string]any) []map[string]any {
	var result []map[string]any
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		desc, _ := tool["description"].(string)
		inputSchema, _ := tool["input_schema"].(map[string]any)

		result = append(result, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": desc,
				"parameters":  inputSchema,
			},
		})
	}
	return result
}

func mapAnthropicModelToLingma(model string) string {
	switch {
	case strings.Contains(model, "sonnet"):
		return "dashscope_qwen3_coder"
	case strings.Contains(model, "haiku"):
		return "dashscope_qmodel"
	case strings.Contains(model, "opus"):
		return "dashscope_qwen_max_latest"
	default:
		return "org_auto"
	}
}

func (h *BridgeHandler) streamAnthropic(ctx context.Context, w http.ResponseWriter, msgID, modelKey string, body map[string]any, maxTokens int) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)

	// Send message_start
	writeAnthropicSSE(w, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":           msgID,
			"type":         "message",
			"role":         "assistant",
			"content":      []any{},
			"model":        modelKey,
			"stop_reason":  nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})
	if canFlush {
		flusher.Flush()
	}

	// Start a content block
	blockIndex := 0
	blockStarted := false
	outputTokens := 0

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		switch event.Type {
		case "data":
			if event.Content != "" {
				if !blockStarted {
					// Start text content block
					writeAnthropicSSE(w, "content_block_start", map[string]any{
						"type":         "content_block_start",
						"index":        blockIndex,
						"content_block": map[string]any{"type": "text", "text": ""},
					})
					blockStarted = true
				}

				writeAnthropicSSE(w, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": blockIndex,
					"delta": map[string]any{
						"type": "text_delta",
						"text": event.Content,
					},
				})
				outputTokens++
				if canFlush {
					flusher.Flush()
				}
			}

			if event.FinishReason != "" {
				if blockStarted {
					writeAnthropicSSE(w, "content_block_stop", map[string]any{
						"type":  "content_block_stop",
						"index": blockIndex,
					})
				}
			}

		case "finish":
			// Determine stop reason
			stopReason := "end_turn"
			// message_delta with usage
			writeAnthropicSSE(w, "message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   stopReason,
					"stop_sequence": nil,
				},
				"usage": map[string]any{
					"output_tokens": outputTokens,
				},
			})
			writeAnthropicSSE(w, "message_stop", map[string]any{
				"type": "message_stop",
			})
			if canFlush {
				flusher.Flush()
			}
		}
		return nil
	})

	if err != nil {
		writeAnthropicSSE(w, "error", map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "api_error",
				"message": err.Error(),
			},
		})
		if canFlush {
			flusher.Flush()
		}
	}
}

func (h *BridgeHandler) nonStreamAnthropic(ctx context.Context, w http.ResponseWriter, msgID, modelKey string, body map[string]any, maxTokens int) {
	var fullContent strings.Builder
	outputTokens := 0

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		if event.Type == "data" && event.Content != "" {
			fullContent.WriteString(event.Content)
			outputTokens++
		}
		return nil
	})

	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	resp := map[string]any{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"model":         modelKey,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"content": []map[string]any{{
			"type": "text",
			"text": fullContent.String(),
		}},
		"usage": map[string]any{
			"input_tokens":  0,
			"output_tokens": outputTokens,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func writeAnthropicSSE(w http.ResponseWriter, event string, data any) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", jsonBytes)
}

func writeAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"error","error":{"type":"%s","message":"%s"}}`, escapeJSON(errType), escapeJSON(message))
}
