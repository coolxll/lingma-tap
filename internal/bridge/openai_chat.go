package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

// modelCache holds cached model data from the Lingma API.
var (
	modelCache      []ModelInfo
	modelCacheTime  time.Time
	modelCacheValid bool
)

// fetchModelsWithCache returns cached models or fetches fresh ones from the API.
func (h *BridgeHandler) fetchModelsWithCache(ctx context.Context) ([]ModelInfo, error) {
	if modelCacheValid && time.Since(modelCacheTime) < 10*time.Minute {
		return modelCache, nil
	}
	models, err := h.client.FetchModels(ctx)
	if err != nil {
		if modelCacheValid {
			return modelCache, nil // stale cache is better than nothing
		}
		return nil, err
	}
	modelCache = models
	modelCacheTime = time.Now()
	modelCacheValid = true
	return models, nil
}

type modelResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	Name    string `json:"name,omitempty"`
}

// HandleModels handles GET /v1/models and GET /v1/models/{id}
func (h *BridgeHandler) HandleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	models, err := h.fetchModelsWithCache(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "Failed to fetch models: "+err.Error())
		return
	}

	// Extract model ID from path: /v1/models or /v1/models/{id}
	path := strings.TrimPrefix(r.URL.Path, "/v1/models")
	path = strings.TrimPrefix(path, "/")

	created := int64(1700000000)

	if path != "" {
		// GET /v1/models/{id} - path is the model key (id field)
		for _, m := range models {
			if m.Key == path {
				fName := friendlyName(m.Key, m.DisplayName)
				resp := modelResponse{
					ID:      m.Key,
					Object:  "model",
					Created: created,
					OwnedBy: "lingma",
					Name:    fName,
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
				return
			}
		}
		writeOpenAIError(w, http.StatusNotFound, fmt.Sprintf("Model '%s' not found", path))
		return
	}

	// GET /v1/models
	var data []modelResponse
	for _, m := range models {
		fName := friendlyName(m.Key, m.DisplayName)
		data = append(data, modelResponse{
			ID:      m.Key,
			Object:  "model",
			Created: created,
			OwnedBy: "lingma",
			Name:    fName,
		})
	}

	resp := map[string]any{
		"object": "list",
		"data":   data,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleOpenAIChat handles POST /v1/chat/completions
func (h *BridgeHandler) HandleOpenAIChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"Method not allowed","type":"invalid_request_error"}}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Model       string           `json:"model"`
		Messages    []map[string]any `json:"messages"`
		Tools       []map[string]any `json:"tools"`
		Stream      bool             `json:"stream"`
		Temperature *float64         `json:"temperature"`
		MaxTokens   *int             `json:"max_tokens"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}

	if len(req.Messages) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "messages is required")
		return
	}

	// Dynamically map model name to Lingma model key
	modelKey := h.resolveModelKey(r.Context(), req.Model)

	// Build parameters
	params := map[string]any{}
	if req.Temperature != nil {
		params["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		params["max_tokens"] = *req.MaxTokens
	}

	// Build Lingma body
	body := BuildLingmaBody(req.Messages, req.Tools, modelKey, params)

	// Generate request ID for OpenAI response
	reqID := "chatcmpl-" + newUUID()[:24]
	created := json.Number(fmt.Sprintf("%d", currentTimeUnix()))

	// Initialize Gateway Log
	gLog := &proto.GatewayLog{
		Ts:          time.Now().Format(time.RFC3339Nano),
		Session:     reqID,
		Model:       modelKey,
		Method:      r.Method,
		Path:        r.URL.Path,
		RequestBody: func() string { b, _ := json.Marshal(body); return string(b) }(),
	}
	startTime := time.Now()

	// Initial save (Request started)
	h.recorder(gLog)

	if req.Stream {
		h.streamOpenAIChat(r.Context(), w, reqID, created, modelKey, body, gLog, startTime)
	} else {
		h.nonStreamOpenAIChat(r.Context(), w, reqID, created, modelKey, body, gLog, startTime)
	}
}

func (h *BridgeHandler) streamOpenAIChat(ctx context.Context, w http.ResponseWriter, reqID string, created json.Number, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)

	// Track tool call state for proper ID management
	toolCallIDs := make(map[int]string)
	var fullContent strings.Builder

	finishSent := false

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		switch event.Type {
		case "data":
			// Skip empty data events (no content, no tool calls, no finish reason)
			if event.Content == "" && len(event.ToolCalls) == 0 && event.FinishReason == "" {
				if event.Usage != nil {
					// Still send usage info as a chunk
					chunk := map[string]any{
						"id":      reqID,
						"object":  "chat.completion.chunk",
						"created": created,
						"model":   modelKey,
						"choices": []map[string]any{{
							"index":         0,
							"delta":         map[string]any{},
							"finish_reason": nil,
						}},
						"usage": event.Usage,
					}
					writeSSE(w, "data: ", chunk)
					if canFlush {
						flusher.Flush()
					}
				}
				return nil
			}

			if event.Content != "" {
				fullContent.WriteString(event.Content)
			}

			chunk := map[string]any{
				"id":      reqID,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   modelKey,
			}

			delta := map[string]any{}
			if event.Content != "" {
				delta["role"] = "assistant"
				delta["content"] = event.Content
			}

			if len(event.ToolCalls) > 0 {
				var toolCalls []map[string]any
				for _, tc := range event.ToolCalls {
					// Generate stable ID for new tool calls
					if tc.ID != "" {
						toolCallIDs[tc.Index] = tc.ID
					}
					tcID := toolCallIDs[tc.Index]
					if tcID == "" {
						tcID = "call_" + newUUID()[:24]
						toolCallIDs[tc.Index] = tcID
					}

					tcObj := map[string]any{
						"index": tc.Index,
						"id":    tcID,
						"type":  "function",
					}
					if tc.Name != "" {
						tcObj["function"] = map[string]any{
							"name":      tc.Name,
							"arguments": tc.Arguments,
						}
					} else if tc.Arguments != "" {
						tcObj["function"] = map[string]any{
							"arguments": tc.Arguments,
						}
					}
					toolCalls = append(toolCalls, tcObj)
				}
				delta["tool_calls"] = toolCalls
			}

			if event.FinishReason != "" {
				finishSent = true
				chunk["choices"] = []map[string]any{{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": event.FinishReason,
				}}
				gLog.FinishReason = event.FinishReason
			} else {
				chunk["choices"] = []map[string]any{{
					"index":         0,
					"delta":         delta,
					"finish_reason": nil,
				}}
			}

			if event.Usage != nil {
				chunk["usage"] = event.Usage
				gLog.InputTokens = event.Usage.PromptTokens
				gLog.OutputTokens = event.Usage.CompletionTokens
			}

			writeSSE(w, "data: ", chunk)
			if canFlush {
				flusher.Flush()
			}

		case "finish":
			if !finishSent {
				chunk := map[string]any{
					"id":      reqID,
					"object":  "chat.completion.chunk",
					"created": created,
					"model":   modelKey,
					"choices": []map[string]any{{
						"index":         0,
						"delta":         map[string]any{},
						"finish_reason": "stop",
					}},
				}
				writeSSE(w, "data: ", chunk)
			}
			io.WriteString(w, "data: [DONE]\n\n")
			if canFlush {
				flusher.Flush()
			}

			// Finalize Log
			gLog.Status = 200
			gLog.ResponseBody = fullContent.String()
			gLog.Latency = time.Since(startTime).Milliseconds()
			if gLog.FinishReason == "" {
				gLog.FinishReason = "stop"
			}
			h.recorder(gLog)

		case "done":
			// Already handled in finish
		}
		return nil
	})

	if err != nil {
		// Error after headers sent — log but can't change status
		gLog.Error = err.Error()
		gLog.Status = 500
		h.recorder(gLog)
		fmt.Fprintf(w, `data: {"error":{"message":"%s","type":"server_error"}}\n\n`, escapeJSON(err.Error()))
		if canFlush {
			flusher.Flush()
		}
	}
}

func (h *BridgeHandler) nonStreamOpenAIChat(ctx context.Context, w http.ResponseWriter, reqID string, created json.Number, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time) {
	var fullContent strings.Builder
	var finishReason string
	var usage *Usage
	var toolCalls map[int]*toolCallState

	err := h.client.ChatStream(ctx, body, func(event SSEEvent) error {
		switch event.Type {
		case "data":
			if event.Content != "" {
				fullContent.WriteString(event.Content)
			}
			if event.FinishReason != "" {
				finishReason = event.FinishReason
			}
			if len(event.ToolCalls) > 0 {
				if toolCalls == nil {
					toolCalls = make(map[int]*toolCallState)
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

	if finishReason == "" {
		finishReason = "stop"
	}

	resp := map[string]any{
		"id":      reqID,
		"object":  "chat.completion",
		"created": created,
		"model":   modelKey,
	}

	choice := map[string]any{
		"index":         0,
		"finish_reason": finishReason,
	}

	if len(toolCalls) > 0 {
		var tcList []map[string]any
		for idx, tc := range toolCalls {
			tcList = append(tcList, map[string]any{
				"id":   tc.id,
				"type": "function",
				"function": map[string]any{
					"name":      tc.name,
					"arguments": tc.args.String(),
				},
				"index": idx,
			})
		}
		choice["message"] = map[string]any{
			"role":      "assistant",
			"content":   nil,
			"tool_calls": tcList,
		}
	} else {
		choice["message"] = map[string]any{
			"role":    "assistant",
			"content": fullContent.String(),
		}
	}

	resp["choices"] = []map[string]any{choice}

	if usage != nil {
		resp["usage"] = usage
	} else {
		resp["usage"] = map[string]any{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		}
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
	gLog.FinishReason = finishReason
	h.recorder(gLog)

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}

type toolCallState struct {
	id   string
	name string
	args strings.Builder
}

// resolveModelKey dynamically maps a requested model string to a Lingma model key
// by checking the fetched models' keys and display names.
func (h *BridgeHandler) resolveModelKey(ctx context.Context, model string) string {
	if model == "" {
		return "org_auto"
	}

	models, err := h.fetchModelsWithCache(ctx)
	if err == nil {
		modelLower := strings.ToLower(model)
		for _, m := range models {
			if strings.ToLower(m.Key) == modelLower {
				return m.Key
			}
			if strings.ToLower(m.DisplayName) == modelLower {
				return m.Key
			}
		}
	}

	// Fallback logic if not found or fetch fails
	modelLower := strings.ToLower(model)
	if modelLower == "org_auto" || modelLower == "auto" {
		return "org_auto"
	}
	return model
}

func writeOpenAIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"message":"%s","type":"invalid_request_error"}}`, escapeJSON(message))
}

func writeSSE(w http.ResponseWriter, prefix string, data any) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "%s%s\n\n", prefix, jsonBytes)
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

func currentTimeUnix() int64 {
	return time.Now().Unix()
}

// friendlyName returns a user-friendly display name for a model key.
// It uses API display name if available, then falls back to key.
func friendlyName(key string, apiDisplayName string) string {
	if apiDisplayName != "" {
		return apiDisplayName
	}
	return key
}
