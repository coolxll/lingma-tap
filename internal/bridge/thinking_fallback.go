package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

const (
	lingmaThinkingFallbackHeaderName       = "X-Lingma-Tap-Fallback"
	lingmaThinkingFallbackHeaderValue      = "lingma-thinking-disabled"
	lingmaThinkingFallbackDefaultTTL       = 2 * time.Minute
	lingmaThinkingFallbackMaxEntries       = 1024
	lingmaThinkingFallbackLargeBodyBytes   = 128 * 1024
	lingmaThinkingFallbackToolHistoryLimit = 20
	lingmaThinkingFallbackToolsLimit       = 40
)

type lingmaRequestProfile struct {
	BodyBytes     int
	Messages      int
	ToolCalls     int
	ToolResults   int
	Tools         int
	LargeThinking bool
}

type lingmaThinkingFallbackDecision struct {
	Key      string
	Eligible bool
	Applied  bool
}

type lingmaUpstreamEventError struct {
	Code    string
	Message string
	Type    string
}

func (e lingmaUpstreamEventError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("lingma upstream error %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("lingma upstream error: %s", e.Message)
}

func errorFromSSEEvent(event SSEEvent) error {
	if !event.HasError {
		return nil
	}
	return lingmaUpstreamEventError{
		Code:    event.ErrorCode,
		Message: orDefault(event.ErrorMsg, "unknown upstream error"),
		Type:    event.ErrorType,
	}
}

func loadLingmaThinkingFallbackConfig() (bool, time.Duration) {
	enabled := true
	if raw := strings.TrimSpace(os.Getenv("LINGMA_THINKING_FALLBACK")); raw != "" {
		switch strings.ToLower(raw) {
		case "0", "false", "off", "no", "disabled":
			enabled = false
		}
	}

	ttl := lingmaThinkingFallbackDefaultTTL
	if raw := strings.TrimSpace(os.Getenv("LINGMA_THINKING_FALLBACK_TTL")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			ttl = parsed
		}
	}

	return enabled, ttl
}

func inspectLingmaRequest(body map[string]any, modelKey string) lingmaRequestProfile {
	profile := lingmaRequestProfile{}
	if body == nil {
		return profile
	}

	bodyJSON, err := json.Marshal(body)
	if err == nil {
		profile.BodyBytes = len(bodyJSON)
	}

	isReasoning := false
	if modelConfig, ok := body["model_config"].(map[string]any); ok {
		if value, ok := modelConfig["is_reasoning"].(bool); ok {
			isReasoning = value
		}
	}

	switch messages := body["messages"].(type) {
	case []map[string]any:
		profile.Messages = len(messages)
		for _, message := range messages {
			profile.ToolCalls += toolCallCount(message["tool_calls"])
			if strings.EqualFold(strings.TrimSpace(roleString(message["role"])), "tool") {
				profile.ToolResults++
			}
		}
	case []any:
		profile.Messages = len(messages)
		for _, item := range messages {
			message, ok := item.(map[string]any)
			if !ok {
				continue
			}
			profile.ToolCalls += toolCallCount(message["tool_calls"])
			if strings.EqualFold(strings.TrimSpace(roleString(message["role"])), "tool") {
				profile.ToolResults++
			}
		}
	}

	switch tools := body["tools"].(type) {
	case []map[string]any:
		profile.Tools = len(tools)
	case []any:
		profile.Tools = len(tools)
	}

	toolHistory := profile.ToolCalls + profile.ToolResults
	profile.LargeThinking = isReasoning &&
		profile.BodyBytes >= lingmaThinkingFallbackLargeBodyBytes &&
		(toolHistory >= lingmaThinkingFallbackToolHistoryLimit || profile.Tools >= lingmaThinkingFallbackToolsLimit)
	return profile
}

func toolCallCount(value any) int {
	switch calls := value.(type) {
	case []map[string]any:
		return len(calls)
	case []any:
		return len(calls)
	default:
		return 0
	}
}

func roleString(value any) string {
	s, _ := value.(string)
	return s
}

func lingmaThinkingFallbackKey(protocol, modelKey string, rawBody []byte) string {
	if len(rawBody) == 0 {
		return ""
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.ToLower(strings.TrimSpace(protocol))))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strings.ToLower(strings.TrimSpace(modelKey))))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(rawBody)
	return hex.EncodeToString(hash.Sum(nil))
}

func (h *BridgeHandler) warnLargeThinkingRequest(modelKey, protocol string, profile lingmaRequestProfile) {
	if h == nil || !profile.LargeThinking {
		return
	}
	log.Printf(
		"[bridge] Lingma large thinking request may stall upstream protocol=%s model=%s body_bytes=%d messages=%d tool_calls=%d tool_results=%d tools=%d",
		protocol,
		modelKey,
		profile.BodyBytes,
		profile.Messages,
		profile.ToolCalls,
		profile.ToolResults,
		profile.Tools,
	)
}

func (h *BridgeHandler) applyThinkingFallback(protocol, modelKey string, rawBody []byte, body map[string]any, profile lingmaRequestProfile) lingmaThinkingFallbackDecision {
	decision := lingmaThinkingFallbackDecision{}
	if h == nil || !h.thinkingFallbackEnabled || h.thinkingFallback == nil || !profile.LargeThinking {
		return decision
	}

	decision.Key = lingmaThinkingFallbackKey(protocol, modelKey, rawBody)
	decision.Eligible = decision.Key != ""
	if !decision.Eligible || !h.thinkingFallback.consume(decision.Key) {
		return decision
	}

	disableLingmaThinking(body)
	decision.Applied = true

	log.Printf("[bridge] Lingma thinking fallback applied protocol=%s model=%s fingerprint=%s", protocol, modelKey, shortFingerprint(decision.Key))
	return decision
}

func (h *BridgeHandler) retryLingmaThinkingFallbackBody(protocol, modelKey string, body map[string]any, profile lingmaRequestProfile, fallback lingmaThinkingFallbackDecision, err error, emittedContent bool) (map[string]any, lingmaThinkingFallbackDecision, bool) {
	if h == nil || !h.thinkingFallbackEnabled || fallback.Applied || emittedContent || err == nil {
		return nil, lingmaThinkingFallbackDecision{}, false
	}
	if !isRetryableLingmaThinkingFallbackError(err) {
		return nil, lingmaThinkingFallbackDecision{}, false
	}
	if !isLingmaUnknownSSEError(err) && !profile.LargeThinking {
		return nil, lingmaThinkingFallbackDecision{}, false
	}

	retryBody, ok := cloneLingmaBody(body)
	if !ok {
		log.Printf("[bridge] Lingma thinking fallback retry skipped protocol=%s model=%s reason=clone_failed", protocol, modelKey)
		return nil, lingmaThinkingFallbackDecision{}, false
	}

	reason := "thinking_disabled"
	if isLingmaUnknownSSEError(err) && redactLatestToolTurn(retryBody) {
		reason = "latest_tool_turn_redacted"
	} else {
		disableLingmaThinking(retryBody)
	}
	retryDecision := fallback
	retryDecision.Applied = true
	log.Printf(
		"[bridge] Lingma thinking fallback retry protocol=%s model=%s reason=%s body_bytes=%d messages=%d tool_calls=%d tool_results=%d tools=%d err=%q",
		protocol,
		modelKey,
		reason,
		profile.BodyBytes,
		profile.Messages,
		profile.ToolCalls,
		profile.ToolResults,
		profile.Tools,
		err.Error(),
	)
	return retryBody, retryDecision, true
}

func isLingmaUnknownSSEError(err error) bool {
	var upstreamErr lingmaUpstreamEventError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	code := strings.TrimSpace(upstreamErr.Code)
	msg := strings.ToLower(upstreamErr.Message)
	return code == "418" || strings.Contains(msg, "unknown sse issue")
}

func cloneLingmaBody(body map[string]any) (map[string]any, bool) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, false
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, false
	}
	return cloned, true
}

func disableLingmaThinking(body map[string]any) {
	if modelConfig, ok := body["model_config"].(map[string]any); ok {
		modelConfig["is_reasoning"] = false
		modelConfig["source"] = ""
	}
	body["agent_id"] = "agent_common"
}

func redactLatestToolTurn(body map[string]any) bool {
	messages := lingmaMessages(body)
	if len(messages) == 0 {
		return false
	}

	for toolIdx := len(messages) - 1; toolIdx >= 0; toolIdx-- {
		toolMsg := messages[toolIdx]
		if !strings.EqualFold(strings.TrimSpace(roleString(toolMsg["role"])), "tool") {
			continue
		}
		toolCallID := strings.TrimSpace(roleString(toolMsg["tool_call_id"]))
		if toolCallID == "" {
			continue
		}
		for assistantIdx := toolIdx - 1; assistantIdx >= 0; assistantIdx-- {
			assistantMsg := messages[assistantIdx]
			if !strings.EqualFold(strings.TrimSpace(roleString(assistantMsg["role"])), "assistant") {
				continue
			}
			if !redactToolCallByID(assistantMsg, toolCallID) {
				break
			}
			toolMsg["content"] = "[tool result omitted by Lingma Tap after upstream rejected the original tool turn]"
			return true
		}
	}
	return false
}

func lingmaMessages(body map[string]any) []map[string]any {
	switch messages := body["messages"].(type) {
	case []map[string]any:
		return messages
	case []any:
		result := make([]map[string]any, 0, len(messages))
		for _, item := range messages {
			message, ok := item.(map[string]any)
			if !ok {
				continue
			}
			result = append(result, message)
		}
		return result
	default:
		return nil
	}
}

func redactToolCallByID(message map[string]any, toolCallID string) bool {
	switch calls := message["tool_calls"].(type) {
	case []map[string]any:
		for _, call := range calls {
			if strings.TrimSpace(roleString(call["id"])) != toolCallID {
				continue
			}
			redactToolCallArguments(call)
			return true
		}
	case []any:
		for _, item := range calls {
			call, ok := item.(map[string]any)
			if !ok || strings.TrimSpace(roleString(call["id"])) != toolCallID {
				continue
			}
			redactToolCallArguments(call)
			return true
		}
	}
	return false
}

func redactToolCallArguments(call map[string]any) {
	if fn, ok := call["function"].(map[string]any); ok {
		fn["arguments"] = `{"redacted":true}`
	}
}

func isRetryableLingmaThinkingFallbackError(err error) bool {
	if err == nil {
		return false
	}
	var upstreamErr lingmaUpstreamEventError
	if errors.As(err, &upstreamErr) {
		code := strings.TrimSpace(upstreamErr.Code)
		msg := strings.ToLower(upstreamErr.Message)
		return code == "418" || strings.Contains(msg, "unknown sse issue")
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "internal_error") ||
		strings.Contains(msg, "stream error") ||
		strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, "closed before [done]")
}

func (h *BridgeHandler) rememberThinkingFallback(err error, decision lingmaThinkingFallbackDecision, profile lingmaRequestProfile, modelKey, protocol string, sawUpstreamEvent bool) {
	if h == nil || !h.thinkingFallbackEnabled || h.thinkingFallback == nil || err == nil || sawUpstreamEvent {
		return
	}
	if !decision.Eligible || decision.Applied || !profile.LargeThinking ||
		(!isContextCanceled(nil, err) && !isLingmaRecoveryCandidate(err)) {
		return
	}
	if !h.thinkingFallback.mark(decision.Key, h.thinkingFallbackTTL) {
		return
	}

	log.Printf(
		"[bridge] Lingma thinking fallback armed protocol=%s model=%s ttl=%s fingerprint=%s",
		protocol,
		modelKey,
		h.thinkingFallbackTTL,
		shortFingerprint(decision.Key),
	)
}

func applyLingmaThinkingRecovery(body map[string]any) {
	if modelConfig, ok := body["model_config"].(map[string]any); ok {
		modelConfig["is_reasoning"] = false
		modelConfig["source"] = ""
	}
	body["agent_id"] = "agent_common"
}

func shortFingerprint(fingerprint string) string {
	if len(fingerprint) <= 12 {
		return fingerprint
	}
	return fingerprint[:12]
}
