package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

// HandleAnthropicMessages handles POST /v1/messages (Anthropic Messages API)
func (h *BridgeHandler) HandleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Method not allowed"}}`, http.StatusMethodNotAllowed)
		return
	}

	// Decode into map first for sanitization flexibility
	var rawReq map[string]any
	if err := json.NewDecoder(r.Body).Decode(&rawReq); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	// Sanitize request: strip thinking blocks, signatures, billing headers
	rawReq = sanitizeAnthropicRequest(rawReq)

	// Extract fields
	model, _ := rawReq["model"].(string)

	var system any
	if s, ok := rawReq["system"]; ok {
		system = s
	}

	var messages []map[string]any
	if m, ok := rawReq["messages"].([]any); ok {
		for _, item := range m {
			if msg, ok := item.(map[string]any); ok {
				messages = append(messages, msg)
			}
		}
	}

	var tools []map[string]any
	if t, ok := rawReq["tools"].([]any); ok {
		for _, item := range t {
			if tool, ok := item.(map[string]any); ok {
				tools = append(tools, tool)
			}
		}
	}

	stream, _ := rawReq["stream"].(bool)

	var maxTokens int
	switch v := rawReq["max_tokens"].(type) {
	case float64:
		maxTokens = int(v)
	case int:
		maxTokens = v
	}

	var temperature *float64
	if t, ok := rawReq["temperature"].(float64); ok {
		temperature = &t
	}

	var topP *float64
	if p, ok := rawReq["top_p"].(float64); ok {
		topP = &p
	}

	stopSequences := anthropicStopSequences(rawReq["stop_sequences"])
	toolChoice := anthropicToolChoiceToOpenAI(rawReq["tool_choice"])

	if len(messages) == 0 {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "messages is required")
		return
	}

	if maxTokens == 0 {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "max_tokens is required")
		return
	}

	// Cap max_tokens to MaxTokensLimit to avoid upstream rejection (Claude Code defaults to 64000)
	if maxTokens > MaxTokensLimit {
		maxTokens = MaxTokensLimit
	}

	modelKey := h.mapAnthropicModelToLingma(r.Context(), model)

	// Convert Anthropic messages to OpenAI format
	messages = anthropicToOpenAIMessages(system, messages)

	// Convert Anthropic tools to OpenAI format
	var openAITools []map[string]any
	if len(tools) > 0 {
		openAITools = anthropicToOpenAITools(tools)
	}

	params := map[string]any{
		"max_tokens": maxTokens,
	}
	if temperature != nil {
		params["temperature"] = *temperature
	}
	if topP != nil {
		params["top_p"] = *topP
	}
	if len(stopSequences) == 1 {
		params["stop"] = stopSequences[0]
	} else if len(stopSequences) > 1 {
		params["stop"] = stopSequences
	}

	// Determine is_reasoning from thinking config
	isReasoning := anthropicIsReasoning(rawReq)

	// Capture raw request JSON for deterministic session_id
	rawReqJSON, _ := json.Marshal(rawReq)

	preparedMessages, imageURLs, visionModel, visionErr := h.prepareVisionRequest(r.Context(), modelKey, messages)
	if visionErr != nil {
		errType := "invalid_request_error"
		if visionErr.Status >= http.StatusInternalServerError {
			errType = "api_error"
		}
		writeAnthropicError(w, visionErr.Status, errType, visionErr.Error())
		return
	}

	body := BuildLingmaBodyWithOptions(preparedMessages, openAITools, modelKey, params, rawReqJSON, LingmaBodyOptions{
		IsReasoning: isReasoning,
		IsVL:        len(imageURLs) > 0,
		ImageURLs:   imageURLs,
		ModelInfo:   visionModel,
		ToolChoice:  toolChoice,
	})
	profile := inspectLingmaRequest(body, modelKey)
	fallback := h.applyThinkingFallback("anthropic_messages", modelKey, rawReqJSON, body, profile)
	if !fallback.Applied {
		h.warnLargeThinkingRequest(modelKey, "anthropic_messages", profile)
	}

	msgID := "msg_" + newUUID()[:24]

	// Initialize Gateway Log
	gLog := &proto.GatewayLog{
		Ts:          time.Now().Format(time.RFC3339Nano),
		Session:     msgID,
		Model:       modelKey,
		Method:      r.Method,
		Path:        r.URL.Path,
		RequestBody: h.captureRequestBody(body),
		IsSSE:       stream,
	}
	startTime := time.Now()
	h.recorder(gLog)

	if stream {
		h.streamAnthropic(r.Context(), w, msgID, modelKey, body, gLog, startTime, profile, fallback)
	} else {
		h.nonStreamAnthropic(r.Context(), w, msgID, modelKey, body, gLog, startTime, profile, fallback)
	}
}

func anthropicToOpenAIMessages(system any, messages []map[string]any) []map[string]any {
	var result []map[string]any

	// Add system message if present
	if system != nil {
		var content any
		switch v := system.(type) {
		case string:
			content = v
		case []any:
			var parts []map[string]any
			for _, block := range v {
				if m, ok := block.(map[string]any); ok {
					if part, ok := convertAnthropicContentPart(m); ok {
						parts = append(parts, part)
					}
				}
			}
			if len(parts) > 0 {
				content = openAIContentPartsOrText(parts)
			}
		}
		if content != nil && content != "" {
			result = append(result, map[string]any{"role": "system", "content": content})
		}
	}

	// Convert messages
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content := msg["content"]

		switch v := content.(type) {
		case string:
			result = append(result, map[string]any{"role": role, "content": v})
		case []any:
			// Array of content blocks - preserve multimodal parts instead of
			// silently dropping image/document blocks.
			switch role {
			case "assistant":
				result = append(result, convertAssistantMessage(v)...)
			case "user":
				textParts, toolResults := convertUserMessage(v)
				result = append(result, toolResults...)
				if len(textParts) > 0 {
					result = append(result, map[string]any{"role": "user", "content": openAIContentPartsOrText(textParts)})
				}
			default:
				// Unknown role, just pass through
				result = append(result, map[string]any{"role": role, "content": content})
			}
		default:
			result = append(result, map[string]any{"role": role, "content": fmt.Sprintf("%v", content)})
		}
	}

	return result
}

// convertAssistantMessage converts an assistant message with content blocks to OpenAI format.
// Returns one or more messages (may include tool_calls).
func convertAssistantMessage(contentBlocks []any) []map[string]any {
	var contentParts []map[string]any
	var toolCalls []map[string]any

	for _, block := range contentBlocks {
		m, ok := block.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := m["type"].(string)
		switch blockType {
		case "text":
			if text, ok := m["text"].(string); ok {
				contentParts = append(contentParts, map[string]any{"type": "text", "text": text})
			}
		case "tool_use":
			id := normalizeAnthropicToolID(stringValue(m["id"]))
			name, _ := m["name"].(string)
			input := m["input"]
			if input == nil {
				input = map[string]any{}
			}
			inputJSON, _ := json.Marshal(input)
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": string(inputJSON),
				},
			})
		default:
			if part, ok := convertAnthropicContentPart(m); ok {
				contentParts = append(contentParts, part)
			}
		}
	}

	// Build the assistant message
	msg := map[string]any{"role": "assistant"}
	if len(contentParts) > 0 {
		msg["content"] = openAIContentPartsOrText(contentParts)
	} else {
		msg["content"] = nil
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	return []map[string]any{msg}
}

// convertUserMessage converts a user message with content blocks to OpenAI format.
// Returns text parts and tool result messages separately.
func convertUserMessage(contentBlocks []any) ([]map[string]any, []map[string]any) {
	var contentParts []map[string]any
	var toolResults []map[string]any

	for _, block := range contentBlocks {
		m, ok := block.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := m["type"].(string)
		switch blockType {
		case "text":
			if text, ok := m["text"].(string); ok {
				contentParts = append(contentParts, map[string]any{"type": "text", "text": text})
			}
		case "tool_result":
			toolUseID := normalizeAnthropicToolID(stringValue(m["tool_use_id"]))
			var resultParts []map[string]any
			if rc, ok := m["content"].(string); ok {
				resultParts = append(resultParts, map[string]any{"type": "text", "text": rc})
			} else if rcBlocks, ok := m["content"].([]any); ok {
				for _, rb := range rcBlocks {
					switch item := rb.(type) {
					case string:
						resultParts = append(resultParts, map[string]any{"type": "text", "text": item})
					case map[string]any:
						if part, ok := convertAnthropicContentPart(item); ok {
							resultParts = append(resultParts, part)
						}
					}
				}
			} else if rcBlock, ok := m["content"].(map[string]any); ok {
				if part, ok := convertAnthropicContentPart(rcBlock); ok {
					resultParts = append(resultParts, part)
				}
			}
			var resultContent any
			if len(resultParts) == 0 {
				resultContent = ""
			} else if len(resultParts) == 1 && resultParts[0]["type"] == "text" {
				resultContent = resultParts[0]["text"]
			} else {
				resultContent = resultParts
			}
			toolResults = append(toolResults, map[string]any{
				"role":         "tool",
				"tool_call_id": toolUseID,
				"content":      resultContent,
			})
		default:
			if part, ok := convertAnthropicContentPart(m); ok {
				contentParts = append(contentParts, part)
			}
		}
	}
	return contentParts, toolResults
}

func openAIContentPartsOrText(parts []map[string]any) any {
	if len(parts) == 1 && parts[0]["type"] == "text" {
		if text, ok := parts[0]["text"].(string); ok {
			return text
		}
	}
	return parts
}

func convertAnthropicContentPart(block map[string]any) (map[string]any, bool) {
	blockType, _ := block["type"].(string)
	switch blockType {
	case "text":
		text, _ := block["text"].(string)
		return map[string]any{"type": "text", "text": text}, true
	case "image":
		url := anthropicSourceURL(block["source"])
		return map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": url},
		}, true
	case "document":
		file := map[string]any{}
		if filename, ok := block["name"].(string); ok && filename != "" {
			file["filename"] = filename
		}
		if source, ok := block["source"].(map[string]any); ok {
			sourceType, _ := source["type"].(string)
			switch sourceType {
			case "base64":
				mediaType, _ := source["media_type"].(string)
				data, _ := source["data"].(string)
				if data != "" {
					if mediaType == "" {
						mediaType = "application/octet-stream"
					}
					file["file_data"] = "data:" + mediaType + ";base64," + data
				}
			case "url":
				if url, ok := source["url"].(string); ok && url != "" {
					file["file_data"] = url
				}
			case "text":
				if text, ok := source["data"].(string); ok && text != "" {
					return map[string]any{"type": "text", "text": text}, true
				}
			}
		}
		if len(file) == 0 {
			return nil, false
		}
		return map[string]any{"type": "file", "file": file}, true
	default:
		return nil, false
	}
}

func anthropicSourceURL(value any) string {
	source, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	switch sourceType, _ := source["type"].(string); sourceType {
	case "url":
		url, _ := source["url"].(string)
		return url
	case "base64":
		data, _ := source["data"].(string)
		if data == "" {
			return ""
		}
		mediaType, _ := source["media_type"].(string)
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		return "data:" + mediaType + ";base64," + data
	default:
		return ""
	}
}

// normalizeAnthropicToolID makes IDs safe for Anthropic's tool_use.id format.
// Keep non-empty IDs stable so a tool_result can refer to the same call.
func normalizeAnthropicToolID(id string) string {
	id = strings.TrimSpace(id)
	var normalized strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			normalized.WriteRune(r)
		default:
			normalized.WriteByte('_')
		}
	}
	if normalized.Len() == 0 {
		return "toolu_" + newUUID()[:24]
	}
	return normalized.String()
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

func anthropicStopSequences(value any) []string {
	switch v := value.(type) {
	case []any:
		stops := make([]string, 0, len(v))
		for _, item := range v {
			if stop, ok := item.(string); ok && stop != "" {
				stops = append(stops, stop)
			}
		}
		return stops
	case []string:
		stops := make([]string, 0, len(v))
		for _, stop := range v {
			if stop != "" {
				stops = append(stops, stop)
			}
		}
		return stops
	default:
		return nil
	}
}

// anthropicToolChoiceToOpenAI maps Anthropic's tool_choice shape to the
// OpenAI-compatible shape consumed by BuildLingmaBody/Lingma.
func anthropicToolChoiceToOpenAI(value any) any {
	choice, ok := value.(map[string]any)
	if !ok {
		return value
	}
	switch typ, _ := choice["type"].(string); typ {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "none":
		return "none"
	case "tool":
		name, _ := choice["name"].(string)
		if name == "" {
			return nil
		}
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name,
			},
		}
	default:
		return value
	}
}

func (h *BridgeHandler) mapAnthropicModelToLingma(ctx context.Context, model string) string {
	modelLower := strings.ToLower(strings.TrimSpace(model))

	// Prefer an exact Lingma model key/display-name match so Anthropic clients can
	// request native Lingma models (for example gm51model) without being forced to
	// the fallback default.
	if modelLower != "" {
		if models, err := h.fetchModelsWithCache(ctx); err == nil {
			for _, m := range models {
				if strings.ToLower(m.Key) == modelLower {
					return m.Key
				}
				if strings.ToLower(m.DisplayName) == modelLower {
					return m.Key
				}
			}
		}
	}

	// Claude-family names still use the user-configurable keyword mapping.
	for keyword, target := range h.modelMapping {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" || target == "" {
			continue
		}
		if strings.Contains(modelLower, strings.ToLower(keyword)) {
			return target
		}
	}

	if h.defaultModel != "" {
		return h.defaultModel
	}
	return DefaultAnthropicModel
}

// sanitizeAnthropicRequest removes fields that may cause upstream rejection:
// - thinking content blocks
// - signature fields in tool_use blocks
// - x-anthropic-billing-header in system messages
// - adjusts budget_tokens if needed
func sanitizeAnthropicRequest(req map[string]any) map[string]any {
	// Sanitize system field
	if system, ok := req["system"]; ok {
		req["system"] = sanitizeSystem(system)
	}

	// Sanitize messages: strip thinking blocks, signatures
	if msgs, ok := req["messages"].([]any); ok {
		var sanitized []any
		for _, m := range msgs {
			if msg, ok := m.(map[string]any); ok {
				sanitized = append(sanitized, sanitizeMessage(msg))
			} else {
				sanitized = append(sanitized, m)
			}
		}
		req["messages"] = sanitized
	}

	// Adjust budget_tokens if present (cap at 2048 to avoid upstream rejection)
	if thinking, ok := req["thinking"].(map[string]any); ok {
		if bt, ok := thinking["budget_tokens"].(float64); ok && bt > 2048 {
			thinking["budget_tokens"] = float64(2048)
		}
	}

	return req
}

// sanitizeSystem strips x-anthropic-billing-header prefix from system messages
func sanitizeSystem(system any) any {
	switch v := system.(type) {
	case string:
		return stripBillingHeader(v)
	case []any:
		var result []any
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						m["text"] = stripBillingHeader(text)
					}
				}
				result = append(result, m)
			} else {
				result = append(result, item)
			}
		}
		return result
	default:
		return system
	}
}

// stripBillingHeader removes the x-anthropic-billing-header line from text
func stripBillingHeader(text string) string {
	prefix := "x-anthropic-billing-header:"
	if !strings.Contains(text, prefix) {
		return text
	}

	lines := strings.Split(text, "\n")
	var result strings.Builder
	result.Grow(len(text))

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			continue
		}
		result.WriteString(line)
		if i < len(lines)-1 {
			result.WriteByte('\n')
		}
	}

	return strings.TrimSpace(result.String())
}

// sanitizeMessage removes thinking blocks and signature fields from a message,
// and also strips billing headers from text content.
func sanitizeMessage(msg map[string]any) map[string]any {
	content := msg["content"]
	switch v := content.(type) {
	case string:
		msg["content"] = stripBillingHeader(v)
	case []any:
		var sanitized []any
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				blockType, _ := m["type"].(string)
				switch blockType {
				case "thinking":
					// Strip thinking blocks entirely
					continue
				case "tool_use":
					// Strip signature field
					delete(m, "signature")
				case "text":
					// Strip billing header from text blocks
					if text, ok := m["text"].(string); ok {
						m["text"] = stripBillingHeader(text)
					}
				}
				sanitized = append(sanitized, m)
			} else {
				sanitized = append(sanitized, item)
			}
		}
		msg["content"] = sanitized
	}
	return msg
}

func (h *BridgeHandler) streamAnthropic(ctx context.Context, w http.ResponseWriter, msgID, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time, profile lingmaRequestProfile, fallback lingmaThinkingFallbackDecision) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if fallback.Applied {
		w.Header().Set(lingmaThinkingFallbackHeaderName, lingmaThinkingFallbackHeaderValue)
	}
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)

	// Send message_start
	writeAnthropicSSE(w, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         modelKey,
			"stop_reason":   nil,
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

	// Streaming state
	state := &anthropicStreamState{
		toolBlocks: make(map[int]*toolBlockState),
	}
	thinkingBlockStarted := false
	thinkingBlockIndex := -1
	var fullReasoning strings.Builder
	var usage *Usage
	stopReason := "end_turn"
	var fullContent strings.Builder
	toolCalls := make(map[int]*toolCallState)
	finalized := false
	recordPayloads := h.shouldRecordPayloads()
	sawUpstreamEvent := false

	// TTFB self-measurement
	var firstTokenTime time.Time
	firstTokenRecorded := false

	stopThinking := func() {
		if thinkingBlockStarted {
			writeAnthropicSSE(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": thinkingBlockIndex,
			})
			thinkingBlockStarted = false
		}
	}
	stopText := func() {
		if state.textBlockStarted {
			writeAnthropicSSE(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": state.textBlockIndex,
			})
			state.textBlockStarted = false
		}
	}

	finalize := func() {
		if finalized {
			return
		}
		finalized = true

		if len(toolCalls) > 0 {
			stopReason = "tool_use"
		}

		// Stop all open content blocks (thinking first, then text, then tools).
		// Tool blocks are sorted by their emitted block index because map iteration
		// order is deliberately random in Go.
		stopThinking()
		stopText()
		toolIndices := make([]int, 0, len(state.toolBlocks))
		for index := range state.toolBlocks {
			toolIndices = append(toolIndices, index)
		}
		sort.Slice(toolIndices, func(i, j int) bool {
			left := state.toolBlocks[toolIndices[i]]
			right := state.toolBlocks[toolIndices[j]]
			if left.blockIndex == right.blockIndex {
				return toolIndices[i] < toolIndices[j]
			}
			return left.blockIndex < right.blockIndex
		})
		for _, index := range toolIndices {
			ts := state.toolBlocks[index]
			if ts.started {
				writeAnthropicSSE(w, "content_block_stop", map[string]any{
					"type":  "content_block_stop",
					"index": ts.blockIndex,
				})
			}
		}

		outTokens := 0
		if usage != nil {
			outTokens = usage.CompletionTokens
		}
		applyUsageToGatewayLog(gLog, usage)

		// message_delta with usage
		writeAnthropicSSE(w, "message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]any{
				"output_tokens": outTokens,
			},
		})
		writeAnthropicSSE(w, "message_stop", map[string]any{
			"type": "message_stop",
		})
		if canFlush {
			flusher.Flush()
		}

		// Finalize Log
		gLog.Status = 200
		gLog.FinishReason = stopReason
		gLog.Latency = time.Since(startTime).Milliseconds()

		// TTFB fallback: use self-measured value when upstream does not send
		// a finish metadata event before [DONE].
		if gLog.TTFT == 0 && firstTokenRecorded {
			gLog.TTFT = firstTokenTime.Sub(startTime).Milliseconds()
		}

		if recordPayloads {
			var content []map[string]any
			if fullReasoning.Len() > 0 {
				content = append(content, map[string]any{"type": "thinking", "thinking": fullReasoning.String()})
			}
			if fullContent.Len() > 0 {
				content = append(content, map[string]any{"type": "text", "text": fullContent.String()})
			}
			toolIndices := make([]int, 0, len(toolCalls))
			for index := range toolCalls {
				toolIndices = append(toolIndices, index)
			}
			sort.Ints(toolIndices)
			for _, index := range toolIndices {
				tc := toolCalls[index]
				var input map[string]any
				if err := json.Unmarshal([]byte(tc.args.String()), &input); err != nil {
					input = map[string]any{"_error": "failed to parse arguments", "raw": tc.args.String()}
				}
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.id,
					"name":  tc.name,
					"input": input,
				})
			}
			respSummary := map[string]any{
				"id":      msgID,
				"role":    "assistant",
				"model":   modelKey,
				"content": content,
				"usage":   usage,
			}
			h.captureResponseBody(gLog, respSummary)
		}
		h.recorder(gLog)
	}

	handleEvent := func(event SSEEvent) error {
		sawUpstreamEvent = true
		switch event.Type {
		case "data":
			// Record first token time for TTFB self-measurement
			if !firstTokenRecorded && (event.Content != "" || event.ReasoningContent != "" || len(event.ToolCalls) > 0) {
				firstTokenTime = time.Now()
				firstTokenRecorded = true
			}

			// Handle reasoning content (thinking blocks)
			if event.ReasoningContent != "" {
				stopText()
				if recordPayloads {
					fullReasoning.WriteString(event.ReasoningContent)
				}
				if !thinkingBlockStarted {
					stopThinking() // defensive: close any stale thinking block
					thinkingBlockIndex = state.nextIndex()
					writeAnthropicSSE(w, "content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": thinkingBlockIndex,
						"content_block": map[string]any{
							"type":     "thinking",
							"thinking": "",
						},
					})
					thinkingBlockStarted = true
				}
				writeAnthropicSSE(w, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": thinkingBlockIndex,
					"delta": map[string]any{
						"type":     "thinking_delta",
						"thinking": event.ReasoningContent,
					},
				})
				if canFlush {
					flusher.Flush()
				}
			}

			// Handle errors
			if event.HasError {
				if err := errorFromSSEEvent(event); err != nil {
					return err
				}
				return nil
			}

			// Handle text content
			if event.Content != "" {
				// Close thinking block before starting text
				stopThinking()
				if recordPayloads {
					fullContent.WriteString(event.Content)
				}
				if !state.textBlockStarted {
					idx := state.nextIndex()
					writeAnthropicSSE(w, "content_block_start", map[string]any{
						"type":          "content_block_start",
						"index":         idx,
						"content_block": map[string]any{"type": "text", "text": ""},
					})
					state.textBlockStarted = true
					state.textBlockIndex = idx
				}

				writeAnthropicSSE(w, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": state.textBlockIndex,
					"delta": map[string]any{
						"type": "text_delta",
						"text": event.Content,
					},
				})
				if canFlush {
					flusher.Flush()
				}
			}

			// Handle tool calls
			if len(event.ToolCalls) > 0 {
				stopThinking()
				stopText()
			}
			deltas := append([]ToolCallDelta(nil), event.ToolCalls...)
			sort.SliceStable(deltas, func(i, j int) bool {
				return deltas[i].Index < deltas[j].Index
			})
			for _, tc := range deltas {
				toolState, ok := state.toolBlocks[tc.Index]
				if !ok {
					// New tool call
					id := normalizeAnthropicToolID(tc.ID)
					state.toolBlockCounter++
					toolState = &toolBlockState{
						id:         id,
						name:       tc.Name,
						blockIndex: state.nextIndex(),
					}
					state.toolBlocks[tc.Index] = toolState
					toolCalls[tc.Index] = &toolCallState{id: id, name: tc.Name}
				}

				if tc.Name != "" {
					toolState.name = tc.Name
					toolCalls[tc.Index].name = tc.Name
				}
				if tc.Arguments != "" {
					if recordPayloads {
						toolCalls[tc.Index].args.WriteString(tc.Arguments)
					}
				}

				// Send content_block_start if not started
				if !toolState.started {
					startEvent := map[string]any{
						"type":  "content_block_start",
						"index": toolState.blockIndex,
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    toolState.id,
							"name":  toolState.name,
							"input": map[string]any{},
						},
					}
					writeAnthropicSSE(w, "content_block_start", startEvent)
					toolState.started = true
				}

				// Send input_json_delta
				if tc.Arguments != "" {
					writeAnthropicSSE(w, "content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": toolState.blockIndex,
						"delta": map[string]any{
							"type":         "input_json_delta",
							"partial_json": tc.Arguments,
						},
					})
					toolState.inputAccum.WriteString(tc.Arguments)
					if canFlush {
						flusher.Flush()
					}
				}
			}

			// Handle finish reason
			if event.FinishReason != "" {
				stopReason = mapFinishReason(event.FinishReason)
			}

			// Handle usage
			if event.Usage != nil {
				usage = event.Usage
				applyUsageToGatewayLog(gLog, usage)
			}

		case "finish":
			applyFinishEvent(gLog, event)
			if event.Usage != nil {
				usage = event.Usage
			}
		case "done":
			finalize()
		}
		return nil
	}

	var err error
	for {
		err = h.chatStream(ctx, body, gLog, handleEvent)
		if err != nil {
			if retryBody, retryFallback, ok := h.retryLingmaThinkingFallbackBody("anthropic_messages", modelKey, body, profile, fallback, err, firstTokenRecorded); ok {
				body = retryBody
				fallback = retryFallback
				continue
			}
		}
		break
	}

	if err != nil {
		h.rememberThinkingFallback(err, fallback, profile, modelKey, "anthropic_messages", sawUpstreamEvent)
		if recordStreamError(ctx, gLog, startTime, err, h.recorder) {
			return
		}

		message := normalizeLingmaUpstreamError(err)
		writeAnthropicSSE(w, "error", map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "api_error",
				"message": message,
			},
		})
		if canFlush {
			flusher.Flush()
		}
		return
	}

	if !finalized {
		finalize()
	}
}

// anthropicStreamState tracks the state of an Anthropic streaming response
type anthropicStreamState struct {
	textBlockStarted bool
	textBlockIndex   int
	toolBlockCounter int
	currentIndex     int                     // Tracks the next content block index
	toolBlocks       map[int]*toolBlockState // key: OpenAI tool call index
	usage            *Usage
}

type toolBlockState struct {
	id         string
	name       string
	inputAccum strings.Builder
	started    bool
	blockIndex int
}

// nextIndex returns the next content block index and increments the counter
func (s *anthropicStreamState) nextIndex() int {
	idx := s.currentIndex
	s.currentIndex++
	return idx
}

// mapFinishReason converts OpenAI finish reasons to Anthropic stop reasons
func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "function_call":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}

// anthropicIsReasoning determines is_reasoning from Anthropic request's thinking config.
func anthropicIsReasoning(req map[string]any) bool {
	// Check thinking.type
	if thinking, ok := req["thinking"].(map[string]any); ok {
		t, _ := thinking["type"].(string)
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "disabled":
			return false
		case "enabled":
			// Check if budget_tokens is 0
			if bt, ok := thinking["budget_tokens"].(float64); ok && bt == 0 {
				return false
			}
			return true
		case "adaptive", "auto":
			return true
		}
	}
	// Default: reasoning enabled
	return true
}

func (h *BridgeHandler) nonStreamAnthropic(ctx context.Context, w http.ResponseWriter, msgID, modelKey string, body map[string]any, gLog *proto.GatewayLog, startTime time.Time, profile lingmaRequestProfile, fallback lingmaThinkingFallbackDecision) {
	var fullContent strings.Builder
	var fullReasoning strings.Builder
	var usage Usage
	var finishReason string
	toolCalls := make(map[int]*toolCallState)
	var lastErr *SSEEvent
	var firstTokenTime time.Time
	firstTokenRecorded := false
	sawUpstreamEvent := false

	err := h.chatStream(ctx, body, gLog, func(event SSEEvent) error {
		sawUpstreamEvent = true
		switch event.Type {
		case "data":
			if event.HasError {
				lastErr = &event
				return nil
			}
			// Record first token time for TTFB self-measurement
			if !firstTokenRecorded && (event.Content != "" || event.ReasoningContent != "" || len(event.ToolCalls) > 0) {
				firstTokenTime = time.Now()
				firstTokenRecorded = true
			}
			if event.ReasoningContent != "" {
				fullReasoning.WriteString(event.ReasoningContent)
			}
			if event.Content != "" {
				fullContent.WriteString(event.Content)
			}
			if event.FinishReason != "" {
				finishReason = event.FinishReason
			}
			for _, tc := range event.ToolCalls {
				if toolCalls[tc.Index] == nil {
					toolCalls[tc.Index] = &toolCallState{id: normalizeAnthropicToolID(tc.ID)}
				}
				if tc.Name != "" {
					toolCalls[tc.Index].name = tc.Name
				}
				toolCalls[tc.Index].args.WriteString(tc.Arguments)
			}
			if event.Usage != nil {
				usage = *event.Usage
				applyUsageToGatewayLog(gLog, &usage)
			}
		case "finish":
			applyFinishEvent(gLog, event)
			if event.Usage != nil {
				usage = *event.Usage
			}
		}
		return nil
	})

	if err != nil {
		if retryBody, retryFallback, ok := h.retryLingmaThinkingFallbackBody("anthropic_messages", modelKey, body, profile, fallback, err, false); ok {
			h.nonStreamAnthropic(ctx, w, msgID, modelKey, retryBody, gLog, startTime, profile, retryFallback)
			return
		}
		h.rememberThinkingFallback(err, fallback, profile, modelKey, "anthropic_messages", sawUpstreamEvent)
		if recordStreamError(ctx, gLog, startTime, err, h.recorder) {
			return
		}
		writeAnthropicError(w, statusForLingmaUpstreamError(err), "api_error", normalizeLingmaUpstreamError(err))
		return
	}

	// Check for upstream error in SSE events
	if lastErr != nil {
		if retryBody, retryFallback, ok := h.retryLingmaThinkingFallbackBody("anthropic_messages", modelKey, body, profile, fallback, errorFromSSEEvent(*lastErr), false); ok {
			h.nonStreamAnthropic(ctx, w, msgID, modelKey, retryBody, gLog, startTime, profile, retryFallback)
			return
		}
		errMsg := lastErr.ErrorMsg
		if errMsg == "" {
			errMsg = "unknown upstream error"
		}
		errType := lastErr.ErrorType
		if errType == "" {
			errType = "api_error"
		}
		gLog.Error = errMsg
		gLog.Status = http.StatusBadGateway
		h.recorder(gLog)
		writeAnthropicError(w, http.StatusBadGateway, errType, errMsg)
		return
	}

	// Build content array
	var content []map[string]any

	// Add thinking content if any
	if fullReasoning.Len() > 0 {
		content = append(content, map[string]any{
			"type":     "thinking",
			"thinking": fullReasoning.String(),
		})
	}

	// Add text content if any
	if fullContent.Len() > 0 {
		content = append(content, map[string]any{
			"type": "text",
			"text": fullContent.String(),
		})
	}

	// Add tool_use content blocks
	toolIndices := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		toolIndices = append(toolIndices, index)
	}
	sort.Ints(toolIndices)
	for _, index := range toolIndices {
		tc := toolCalls[index]
		tc.id = normalizeAnthropicToolID(tc.id)
		var input map[string]any
		if tc.args.Len() > 0 {
			if err := json.Unmarshal([]byte(tc.args.String()), &input); err != nil {
				input = map[string]any{}
			}
		} else {
			input = map[string]any{}
		}
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    tc.id,
			"name":  tc.name,
			"input": input,
		})
	}

	// Determine stop reason
	stopReason := mapFinishReason(finishReason)
	// If tool calls are present, Claude clients expect tool_use regardless of
	// whether Lingma sent no finish reason or a generic stop reason.
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}

	resp := map[string]any{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"model":         modelKey,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"content":       content,
		"usage": map[string]any{
			"input_tokens":  usage.PromptTokens,
			"output_tokens": usage.CompletionTokens,
		},
	}

	respBytes, _ := json.Marshal(resp)

	// Finalize Log
	gLog.Status = 200
	h.captureResponseBytes(gLog, respBytes)
	applyUsageToGatewayLog(gLog, &usage)
	gLog.Latency = time.Since(startTime).Milliseconds()
	gLog.FinishReason = stopReason

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
