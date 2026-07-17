package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

// HandleOpenAIResponses handles POST /v1/responses (OpenAI Responses API)
func (h *BridgeHandler) HandleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeResponsesError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "Method not allowed", "")
		return
	}

	// Read raw body for deterministic session_id
	rawBody, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(rawBody))

	var req responsesRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "invalid_json", "Invalid request body: "+err.Error(), "")
		return
	}

	// Build messages from input
	messages, warnings := responsesBuildMessages(&req)
	// Check if we have actual user input (not just system instructions)
	// Accept user messages OR tool results (for function_call_output continuation)
	hasUserInput := false
	for _, msg := range messages {
		if role, ok := msg["role"].(string); ok && (role == "user" || role == "tool") {
			hasUserInput = true
			break
		}
	}
	if !hasUserInput {
		writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "missing_input", "input is required", "input")
		return
	}
	if req.PreviousResponseID != "" && h.responsesStore == nil {
		writeResponsesError(w, http.StatusServiceUnavailable, "server_error", "state_store_unavailable", "Conversation state is unavailable", "previous_response_id")
		return
	}

	// Save current turn messages before prepending history (for state persistence).
	// Strip the leading system/instructions message — instructions are saved separately
	// and must not be re-included as history on the next turn.
	currentTurnMessages := messages
	if req.Instructions != "" && len(currentTurnMessages) > 0 {
		if role, _ := currentTurnMessages[0]["role"].(string); role == "system" {
			// Only strip if this is the instructions message (first system message)
			// Check if content matches instructions to avoid stripping user-provided system messages
			if content, ok := currentTurnMessages[0]["content"].(string); ok && content == req.Instructions {
				currentTurnMessages = currentTurnMessages[1:]
			}
		}
	}

	// Compute UID digest once for multi-turn support
	uidDigest := ""
	currentTurnStart := 0
	if h.session != nil {
		uidDigest = h.session.UID
	}

	// Handle multi-turn conversation via previous_response_id
	if req.PreviousResponseID != "" && h.responsesStore != nil {
		if uidDigest == "" {
			writeResponsesError(w, http.StatusForbidden, "invalid_request_error", "no_session", "No active session for multi-turn conversation", "previous_response_id")
			return
		}
		chain, err := h.responsesStore.WalkChain(req.PreviousResponseID, uidDigest)
		if err != nil {
			var status int
			var code string
			switch err {
			case ErrResponseNotFound:
				status = http.StatusNotFound
				code = "response_not_found"
			case ErrResponseExpired:
				status = http.StatusGone
				code = "response_expired"
			case ErrResponseNotCompleted:
				status = http.StatusConflict
				code = "response_not_completed"
			case ErrResponseUIDMismatch:
				status = http.StatusForbidden
				code = "response_uid_mismatch"
			case ErrChainIncomplete:
				status = http.StatusConflict
				code = "chain_incomplete"
			default:
				status = http.StatusInternalServerError
				code = "state_store_error"
			}
			writeResponsesError(w, status, "invalid_request_error", code, err.Error(), "previous_response_id")
			return
		}

		// Prepend conversation history (excluding instructions from previous turns)
		var historyMessages []map[string]any
		for _, entry := range chain {
			var inputMsgs []map[string]any
			if err := json.Unmarshal([]byte(entry.InputJSON), &inputMsgs); err != nil {
				log.Printf("[bridge] failed to unmarshal input messages from state %s: %v", entry.ResponseID, err)
				writeResponsesError(w, http.StatusInternalServerError, "server_error", "corrupted_state", "Failed to load conversation history", "previous_response_id")
				return
			}
			historyMessages = append(historyMessages, inputMsgs...)

			var outputMsgs []map[string]any
			if err := json.Unmarshal([]byte(entry.OutputJSON), &outputMsgs); err != nil {
				log.Printf("[bridge] failed to unmarshal output messages from state %s: %v", entry.ResponseID, err)
				writeResponsesError(w, http.StatusInternalServerError, "server_error", "corrupted_state", "Failed to load conversation history", "previous_response_id")
				return
			}
			// Convert Responses API format to Chat Completions format
			var pendingToolCalls []map[string]any
			for _, msg := range outputMsgs {
				msgType, _ := msg["type"].(string)
				switch msgType {
				case "message":
					// Flush any pending tool calls first
					if len(pendingToolCalls) > 0 {
						historyMessages = append(historyMessages, map[string]any{
							"role":       "assistant",
							"content":    nil,
							"tool_calls": pendingToolCalls,
						})
						pendingToolCalls = nil
					}
					// Convert message format
					chatMsg := map[string]any{
						"role":    msg["role"],
						"content": msg["content"],
					}
					historyMessages = append(historyMessages, chatMsg)

				case "reasoning":
					// Flush any pending tool calls first
					if len(pendingToolCalls) > 0 {
						historyMessages = append(historyMessages, map[string]any{
							"role":       "assistant",
							"content":    nil,
							"tool_calls": pendingToolCalls,
						})
						pendingToolCalls = nil
					}
					// Convert reasoning to assistant message
					chatMsg := map[string]any{
						"role":    "assistant",
						"content": msg["content"],
					}
					historyMessages = append(historyMessages, chatMsg)

				case "function_call":
					// Accumulate tool calls
					name, _ := msg["name"].(string)
					// Apply namespace tool renaming
					if strings.Contains(name, ".") {
						name = strings.ReplaceAll(name, ".", "__")
					}
					callID, _ := msg["call_id"].(string)
					arguments, _ := msg["arguments"].(string)
					pendingToolCalls = append(pendingToolCalls, map[string]any{
						"id":   callID,
						"type": "function",
						"function": map[string]any{
							"name":      name,
							"arguments": arguments,
						},
					})
				}
			}
			// Flush any remaining tool calls
			if len(pendingToolCalls) > 0 {
				historyMessages = append(historyMessages, map[string]any{
					"role":       "assistant",
					"content":    nil,
					"tool_calls": pendingToolCalls,
				})
			}
		}
		currentTurnStart = len(historyMessages)
		messages = append(historyMessages, messages...)
	}

	modelKey := h.resolveModelKey(r.Context(), req.Model)

	// Convert tools
	tools, toolWarnings := responsesConvertTools(req.Tools)
	warnings = append(warnings, toolWarnings...)

	// Build parameters
	params := map[string]any{}
	if req.Temperature != nil {
		params["temperature"] = *req.Temperature
	}
	if req.MaxOutputTokens != nil {
		params["max_tokens"] = min(*req.MaxOutputTokens, MaxTokensLimit)
	}
	if req.ParallelToolCalls != nil {
		params["parallel_tool_calls"] = *req.ParallelToolCalls
	}

	// Determine reasoning effort (nested takes precedence)
	reasoningEffort := req.ReasoningEffort
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		reasoningEffort = req.Reasoning.Effort
	}
	isReasoning := openaiIsReasoningForModel(modelKey, reasoningEffort)

	// Parse tool_choice
	var toolChoice any
	if len(req.ToolChoice) > 0 {
		if err := json.Unmarshal(req.ToolChoice, &toolChoice); err != nil {
			writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "invalid_tool_choice", "Invalid tool_choice: "+err.Error(), "tool_choice")
			return
		}
	}

	// Validate cheap request fields before reading or uploading image input.
	preparedMessages, imageURLs, visionModel, visionErr := h.prepareVisionRequest(r.Context(), modelKey, messages)
	if visionErr != nil {
		errType := "invalid_request_error"
		if visionErr.Status >= http.StatusInternalServerError {
			errType = "server_error"
		}
		writeResponsesError(w, visionErr.Status, errType, visionErr.Code, visionErr.Error(), "input")
		return
	}
	messages = preparedMessages

	responseConfig := responsesResponseConfig{
		MaxOutputTokens:   req.MaxOutputTokens,
		ParallelToolCalls: true,
		Store:             true,
		Temperature:       req.Temperature,
		ToolChoice:        toolChoice,
		Input:             req.Input,
	}
	if req.ParallelToolCalls != nil {
		responseConfig.ParallelToolCalls = *req.ParallelToolCalls
	}
	if req.Store != nil {
		responseConfig.Store = *req.Store
	}

	// Apply context trimming if needed
	trimmer := newResponsesContextTrimmer(DefaultMaxContextBytes)
	originalSize := trimmer.calculateSize(messages)
	messages, trimmed, trimErr := trimmer.trimContextAt(messages, currentTurnStart)
	if trimErr != nil {
		writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "context_too_large", "Current round exceeds context limit", "input")
		return
	}
	trimmedSize := trimmer.calculateSize(messages)

	body := BuildLingmaBodyWithOptions(messages, tools, modelKey, params, rawBody, LingmaBodyOptions{
		IsReasoning: isReasoning,
		IsVL:        len(imageURLs) > 0,
		ImageURLs:   imageURLs,
		ModelInfo:   visionModel,
		ToolChoice:  toolChoice,
	})
	profile := inspectLingmaRequest(body, modelKey)
	fallback := h.applyThinkingFallback("openai_responses", modelKey, rawBody, body, profile)
	if !fallback.Applied {
		h.warnLargeThinkingRequest(modelKey, "openai_responses", profile)
	}

	respID := "resp_" + newUUID()[:24]

	// Initialize Gateway Log
	gLog := &proto.GatewayLog{
		Ts:                   time.Now().Format(time.RFC3339Nano),
		Session:              respID,
		Model:                modelKey,
		ContextTrimmed:       trimmed,
		ContextOriginalBytes: originalSize,
		ContextTrimmedBytes:  trimmedSize,
		Method:               r.Method,
		Path:                 r.URL.Path,
		RequestBody:          h.captureRequestBody(body),
		IsSSE:                req.Stream,
	}
	if trimmed || len(warnings) > 0 {
		gLog.ResponsesDegraded = true
		degradedWarnings := warnings
		if trimmed {
			degradedWarnings = append([]string{"context_trimmed"}, degradedWarnings...)
		}
		// Use JSON encoding to avoid ambiguity with commas in warning messages
		if warningsJSON, err := json.Marshal(degradedWarnings); err == nil {
			gLog.ResponsesWarnings = string(warningsJSON)
		}
	}
	startTime := time.Now()
	h.recorder(gLog)

	// Prepare state for multi-turn support
	// Use currentTurnMessages captured before history was prepended
	inputMessages := currentTurnMessages

	if req.Stream {
		h.streamResponses(r.Context(), w, respID, req.PreviousResponseID, uidDigest, inputMessages, req.Instructions, warnings, modelKey, body, gLog, startTime, profile, fallback, trimmed, req.Tools, req.Reasoning, responseConfig)
	} else {
		h.nonStreamResponses(r.Context(), w, respID, req.PreviousResponseID, uidDigest, inputMessages, req.Instructions, warnings, modelKey, body, gLog, startTime, profile, fallback, trimmed, req.Tools, req.Reasoning, responseConfig)
	}
}

// responsesBuildMessages converts a Responses API request into OpenAI-style messages.
// It returns the message list and any warning codes encountered during conversion.
func responsesBuildMessages(req *responsesRequest) ([]map[string]any, []string) {
	var messages []map[string]any
	var warnings []string

	// 1. Instructions → system message (prepended)
	if req.Instructions != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": req.Instructions,
		})
	}

	// 2. Parse input
	if len(req.Input) == 0 {
		return messages, warnings
	}

	// Try string input first
	var inputStr string
	if err := json.Unmarshal(req.Input, &inputStr); err == nil {
		if inputStr != "" {
			messages = append(messages, map[string]any{"role": "user", "content": inputStr})
		}
		return messages, warnings
	}

	// Array input
	var items []map[string]any
	if err := json.Unmarshal(req.Input, &items); err != nil {
		return messages, warnings
	}

	// Process array items, merging consecutive function_calls
	var pendingToolCalls []map[string]any

	flushToolCalls := func() {
		if len(pendingToolCalls) > 0 {
			messages = append(messages, map[string]any{
				"role":       "assistant",
				"content":    nil,
				"tool_calls": pendingToolCalls,
			})
			pendingToolCalls = nil
		}
	}

	for _, item := range items {
		itemType, _ := item["type"].(string)
		switch itemType {
		case "function_call":
			// Accumulate into pending tool calls (will be merged with consecutive ones)
			id, _ := item["call_id"].(string)
			name, _ := item["name"].(string)
			args, _ := item["arguments"].(string)
			if id == "" {
				id = "call_" + newUUID()[:24]
			}
			pendingToolCalls = append(pendingToolCalls, map[string]any{
				"id":       id,
				"type":     "function",
				"function": map[string]any{"name": name, "arguments": args},
			})

		case "function_call_output":
			// Flush any pending tool calls before adding tool results
			flushToolCalls()
			callID, _ := item["call_id"].(string)
			output := responsesToolOutputString(item["output"])
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      output,
			})

		case "input_text":
			flushToolCalls()
			text, _ := item["text"].(string)
			if text != "" {
				messages = append(messages, map[string]any{"role": "user", "content": text})
			}

		case "output_text":
			flushToolCalls()
			text, _ := item["text"].(string)
			if text != "" {
				messages = append(messages, map[string]any{"role": "assistant", "content": text})
			}

		case "reasoning":
			// Reasoning items from previous turns - preserve as assistant messages
			flushToolCalls()
			content, _ := item["content"].(string)
			if content != "" {
				messages = append(messages, map[string]any{"role": "assistant", "content": content})
			}

		case "input_image":
			flushToolCalls()
			imageURL, _ := item["image_url"].(string)
			if imageURL == "" {
				imageURL, _ = item["url"].(string)
			}
			detail, _ := item["detail"].(string)
			if detail == "" {
				detail = "auto"
			}
			if imageURL != "" {
				messages = append(messages, map[string]any{
					"role": "user",
					"content": []map[string]any{
						{
							"type":      "image_url",
							"image_url": map[string]any{"url": imageURL, "detail": detail},
						},
					},
				})
			} else {
				marker := map[string]any{"type": "input_image"}
				if fileID, _ := item["file_id"].(string); fileID != "" {
					marker["file_id"] = fileID
				}
				messages = append(messages, map[string]any{"role": "user", "content": []map[string]any{marker}})
			}

		case "message":
			flushToolCalls()
			role, _ := item["role"].(string)
			if role == "" {
				role = "user"
			}
			// Normalize developer → system
			if role == "developer" {
				role = "system"
			}
			msg := map[string]any{"role": role}
			if content, ok := item["content"]; ok {
				// Convert content array from Responses API format to Chat Completions format
				if contentArray, isArray := content.([]any); isArray {
					var chatContent []map[string]any
					for _, part := range contentArray {
						if partMap, ok := part.(map[string]any); ok {
							partType, _ := partMap["type"].(string)
							switch partType {
							case "input_text", "output_text":
								if text, ok := partMap["text"].(string); ok {
									chatContent = append(chatContent, map[string]any{
										"type": "text",
										"text": text,
									})
								}
							case "input_image":
								imageURL, _ := partMap["image_url"].(string)
								if imageURL == "" {
									imageURL, _ = partMap["url"].(string)
								}
								detail, _ := partMap["detail"].(string)
								if detail == "" {
									detail = "auto"
								}
								if imageURL != "" {
									chatContent = append(chatContent, map[string]any{
										"type": "image_url",
										"image_url": map[string]any{
											"url":    imageURL,
											"detail": detail,
										},
									})
								} else {
									marker := map[string]any{"type": "input_image"}
									if fileID, _ := partMap["file_id"].(string); fileID != "" {
										marker["file_id"] = fileID
									}
									chatContent = append(chatContent, marker)
								}
							}
						}
					}
					// If only one text part, simplify to string
					if len(chatContent) == 1 && chatContent[0]["type"] == "text" {
						msg["content"] = chatContent[0]["text"]
					} else if len(chatContent) > 0 {
						msg["content"] = chatContent
					}
				} else {
					msg["content"] = content
				}
			} else if text, ok := item["text"].(string); ok {
				msg["content"] = text
			}
			messages = append(messages, msg)

		default:
			// Handle message-like items with a role field (backward compat)
			flushToolCalls()
			role, hasRole := item["role"].(string)
			if hasRole {
				if role == "developer" {
					role = "system"
				}
				msg := map[string]any{"role": role}
				if content, ok := item["content"]; ok {
					msg["content"] = content
				} else if text, ok := item["text"].(string); ok {
					msg["content"] = text
				}
				messages = append(messages, msg)
			} else if itemType != "" {
				warnings = append(warnings, "unsupported_input_item:"+itemType)
			}
		}
	}

	// Flush any remaining pending tool calls
	flushToolCalls()

	return messages, warnings
}

// responsesToolOutputString preserves structured tool results instead of
// silently dropping them when the Responses input uses an object or array.
// Chat Completions tool messages still carry content as a string, so non-string
// values are encoded as JSON.
func responsesToolOutputString(value any) string {
	if value == nil {
		return ""
	}
	if output, ok := value.(string); ok {
		return output
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// responsesConvertTools converts Responses API tool definitions to Chat Completions format.
// Returns the converted tools and any warning codes.
func responsesConvertTools(tools []responsesTool) ([]map[string]any, []string) {
	if len(tools) == 0 {
		return nil, nil
	}

	var result []map[string]any
	var warnings []string

	for _, t := range tools {
		// Skip unsupported tool types
		if t.Type != "" && t.Type != "function" {
			warnings = append(warnings, "unsupported_tool_type:"+t.Type)
			continue
		}

		name := t.Name
		// Namespace tool: reversible qualified-name mapping (ns.func → ns__func)
		if strings.Contains(name, ".") {
			original := name
			name = strings.ReplaceAll(name, ".", "__")
			warnings = append(warnings, "namespace_tool_renamed:"+original+"->"+name)
		}

		if name == "" {
			warnings = append(warnings, "invalid_tool_definition:missing_name")
			continue
		}

		tool := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name,
			},
		}
		fn := tool["function"].(map[string]any)
		if t.Description != "" {
			fn["description"] = t.Description
		}
		if t.Parameters != nil {
			fn["parameters"] = t.Parameters
		}
		if t.Strict != nil {
			fn["strict"] = *t.Strict
		}

		result = append(result, tool)
	}

	return result, warnings
}

// saveResponseState persists the response state for multi-turn conversation support
func (h *BridgeHandler) saveResponseState(respID, parentID, uidDigest string, inputMessages, outputMessages []map[string]any, instructions string, warnings []string, status string, response *responsesResponse) {
	if h.responsesStore == nil {
		return
	}

	inputJSON, err := json.Marshal(inputMessages)
	if err != nil {
		log.Printf("[bridge] failed to marshal input messages for state %s: %v", respID, err)
		return
	}
	outputJSON, err := json.Marshal(outputMessages)
	if err != nil {
		log.Printf("[bridge] failed to marshal output messages for state %s: %v", respID, err)
		return
	}
	warningsJSON, err := json.Marshal(warnings)
	if err != nil {
		log.Printf("[bridge] failed to marshal warnings for state %s: %v", respID, err)
		return
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		log.Printf("[bridge] failed to marshal response for state %s: %v", respID, err)
		return
	}

	entry := &ResponsesStateEntry{
		ResponseID:   respID,
		ParentID:     parentID,
		UIDDigest:    uidDigest,
		Status:       status,
		InputJSON:    string(inputJSON),
		OutputJSON:   string(outputJSON),
		ResponseJSON: string(responseJSON),
		Instructions: instructions,
		WarningsJSON: string(warningsJSON),
		CreatedAt:    time.Now().Format(time.RFC3339Nano),
	}

	if err := h.responsesStore.SaveResponse(entry); err != nil {
		log.Printf("[bridge] failed to save response state %s: %v", respID, err)
	}
}

func (h *BridgeHandler) streamResponses(ctx context.Context, w http.ResponseWriter, respID, parentID, uidDigest string, inputMessages []map[string]any, instructions string, warnings []string, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time, profile lingmaRequestProfile, fallback lingmaThinkingFallbackDecision, contextTrimmed bool, tools []responsesTool, reasoning *responsesReasoning, responseConfig responsesResponseConfig) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if fallback.Applied {
		w.Header().Set(lingmaThinkingFallbackHeaderName, lingmaThinkingFallbackHeaderValue)
	}
	// Set degradation headers for all warnings
	if contextTrimmed || len(warnings) > 0 {
		w.Header().Set("X-Lingma-Tap-Responses-Degraded", "true")
		degradedWarnings := warnings
		if contextTrimmed {
			degradedWarnings = append([]string{"context_trimmed"}, degradedWarnings...)
		}
		if warningsJSON, err := json.Marshal(degradedWarnings); err == nil {
			w.Header().Set("X-Lingma-Tap-Responses-Warnings", string(warningsJSON))
		}
	}
	w.WriteHeader(http.StatusOK)

	agg := newResponsesAggregator(respID, modelKey, startTime, instructions, parentID, tools, reasoning, responseConfig)
	sm := newResponsesStreamMachine(agg, w)
	sm.headersSent = true

	// Send response.created and response.in_progress
	sm.emitCreated()

	sawUpstreamEvent := false

	handleEvent := func(event SSEEvent) error {
		sawUpstreamEvent = true
		switch event.Type {
		case "data":
			if event.HasError {
				if err := errorFromSSEEvent(event); err != nil {
					agg.upstreamErrored = true
					return err
				}
				// errorFromSSEEvent returned nil but HasError was true - this is an error state
				agg.upstreamErrored = true
				return fmt.Errorf("upstream error event received but could not be parsed")
			}
			if event.Usage != nil {
				agg.setUsage(event.Usage)
				applyUsageToGatewayLog(gLog, event.Usage)
			}
			sm.onDataEvent(event)

		case "finish":
			if agg.upstreamErrored {
				return nil
			}
			applyFinishEvent(gLog, event)
			if event.Usage != nil {
				agg.setUsage(event.Usage)
			}

		case "done":
			if agg.upstreamErrored {
				return nil
			}
			if gLog.TTFT == 0 && agg.firstTokenRecorded {
				gLog.TTFT = agg.firstTokenTime.Sub(startTime).Milliseconds()
			}

			// Emit all closing events and response.completed first. This finalizes the
			// aggregator items while preserving the terminal SSE events.
			resp := sm.emitDone()

			// Snapshot the finalized output for state persistence after emitDone has
			// closed all active blocks and emitted their completion events.
			outputMessages := agg.buildOutputMessages()

			gLog.Status = 200
			gLog.Latency = time.Since(startTime).Milliseconds()
			applyUsageToGatewayLog(gLog, agg.usage)

			if h.shouldRecordPayloads() {
				h.captureResponseBody(gLog, resp)
			}
			h.recorder(gLog)

			// Save response state for multi-turn support
			h.saveResponseState(respID, parentID, uidDigest, inputMessages, outputMessages, instructions, warnings, "completed", resp)
		}
		return nil
	}

	var err error
	for {
		err = h.chatStream(ctx, body, gLog, handleEvent)
		if err != nil {
			// Check if actual content was streamed (not just errors)
			emittedContent := agg.firstTokenRecorded || agg.activeText != nil || agg.activeReason != nil || len(agg.toolCalls) > 0
			// Don't retry if content was already streamed - would break SSE stream with duplicate indices
			if emittedContent {
				break
			}
			if retryBody, retryFallback, ok := h.retryLingmaThinkingFallbackBody("openai_responses", modelKey, body, profile, fallback, err, emittedContent); ok {
				body = retryBody
				fallback = retryFallback
				// Reset aggregation state for retry, but continue the already-emitted
				// sequence number after response.created/in_progress. Re-emitting those
				// events is intentionally avoided, so resetting sequenceNum to zero
				// would produce duplicate/non-monotonic SSE sequence numbers.
				sequenceNum := agg.sequenceNum
				agg = newResponsesAggregator(respID, modelKey, startTime, instructions, parentID, tools, reasoning, responseConfig)
				agg.sequenceNum = sequenceNum
				sm = newResponsesStreamMachine(agg, w)
				sm.headersSent = true
				sawUpstreamEvent = false
				// Don't re-emit created/in_progress events - they were already sent on first attempt
				continue
			}
		}
		break
	}

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

		sm.emitFailed(message)
	}
}

func (h *BridgeHandler) nonStreamResponses(ctx context.Context, w http.ResponseWriter, respID, parentID, uidDigest string, inputMessages []map[string]any, instructions string, warnings []string, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time, profile lingmaRequestProfile, fallback lingmaThinkingFallbackDecision, contextTrimmed bool, tools []responsesTool, reasoning *responsesReasoning, responseConfig responsesResponseConfig) {
	agg := newResponsesAggregator(respID, modelKey, startTime, instructions, parentID, tools, reasoning, responseConfig)
	var lastErr *SSEEvent
	sawUpstreamEvent := false

	err := h.chatStream(ctx, body, gLog, func(event SSEEvent) error {
		sawUpstreamEvent = true
		switch event.Type {
		case "data":
			if event.HasError {
				lastErr = &event
				return nil
			}
			// Feed into shared aggregator
			if event.ReasoningContent != "" {
				agg.addReasoningDelta(event.ReasoningContent)
			}
			if event.Content != "" {
				agg.addTextDelta(event.Content)
			}
			for _, tc := range event.ToolCalls {
				agg.addToolCall(tc.Index, tc.ID, tc.Name, tc.Arguments)
			}
			if event.Usage != nil {
				agg.setUsage(event.Usage)
				applyUsageToGatewayLog(gLog, event.Usage)
			}
		case "finish":
			applyFinishEvent(gLog, event)
			if event.Usage != nil {
				agg.setUsage(event.Usage)
			}
		}
		return nil
	})

	if err != nil {
		if retryBody, retryFallback, ok := h.retryLingmaThinkingFallbackBody("openai_responses", modelKey, body, profile, fallback, err, false); ok {
			h.nonStreamResponses(ctx, w, respID, parentID, uidDigest, inputMessages, instructions, warnings, modelKey, retryBody, gLog, startTime, profile, retryFallback, contextTrimmed, tools, reasoning, responseConfig)
			return
		}
		h.rememberThinkingFallback(err, fallback, profile, modelKey, "openai_responses", sawUpstreamEvent)
		if recordStreamError(ctx, gLog, startTime, err, h.recorder) {
			return
		}
		writeResponsesError(w, statusForLingmaUpstreamError(err), "server_error", "upstream_error", normalizeLingmaUpstreamError(err), "")
		return
	}

	// Check for upstream error in SSE events
	if lastErr != nil {
		if retryBody, retryFallback, ok := h.retryLingmaThinkingFallbackBody("openai_responses", modelKey, body, profile, fallback, errorFromSSEEvent(*lastErr), false); ok {
			h.nonStreamResponses(ctx, w, respID, parentID, uidDigest, inputMessages, instructions, warnings, modelKey, retryBody, gLog, startTime, profile, retryFallback, contextTrimmed, tools, reasoning, responseConfig)
			return
		}
		errMsg := orDefault(lastErr.ErrorMsg, "unknown upstream error")
		gLog.Error = errMsg
		gLog.Status = http.StatusBadGateway
		gLog.Latency = time.Since(startTime).Milliseconds()
		h.recorder(gLog)
		writeResponsesError(w, http.StatusBadGateway, "server_error", "upstream_error", errMsg, "")
		return
	}

	// Capture output messages for state persistence BEFORE buildResponse drains toolCalls
	outputMessages := agg.buildOutputMessages()

	// Build response using shared aggregator
	resp := agg.buildResponse("completed")
	respBytes, err := json.Marshal(resp)
	if err != nil {
		writeResponsesError(w, http.StatusInternalServerError, "server_error", "marshal_error", "Failed to serialize response", "")
		return
	}

	// Finalize Log
	gLog.Status = 200
	h.captureResponseBytes(gLog, respBytes)
	applyUsageToGatewayLog(gLog, agg.usage)
	gLog.Latency = time.Since(startTime).Milliseconds()

	// TTFB fallback: use self-measured value if upstream didn't provide one
	if gLog.TTFT == 0 && agg.firstTokenRecorded {
		gLog.TTFT = agg.firstTokenTime.Sub(startTime).Milliseconds()
	}

	h.recorder(gLog)

	// Save response state for multi-turn support
	h.saveResponseState(respID, parentID, uidDigest, inputMessages, outputMessages, instructions, warnings, "completed", resp)

	// Set headers before writing response
	if fallback.Applied {
		w.Header().Set(lingmaThinkingFallbackHeaderName, lingmaThinkingFallbackHeaderValue)
	}
	// Set degradation headers for all warnings
	if contextTrimmed || len(warnings) > 0 {
		w.Header().Set("X-Lingma-Tap-Responses-Degraded", "true")
		degradedWarnings := warnings
		if contextTrimmed {
			degradedWarnings = append([]string{"context_trimmed"}, degradedWarnings...)
		}
		if warningsJSON, err := json.Marshal(degradedWarnings); err == nil {
			w.Header().Set("X-Lingma-Tap-Responses-Warnings", string(warningsJSON))
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}
