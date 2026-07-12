package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	if !strings.EqualFold(strings.TrimSpace(modelKey), "gm51model") || body == nil {
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
		toolHistory >= lingmaThinkingFallbackToolHistoryLimit
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

	applyLingmaThinkingRecovery(body)
	decision.Applied = true

	log.Printf("[bridge] Lingma thinking fallback applied protocol=%s model=%s fingerprint=%s", protocol, modelKey, shortFingerprint(decision.Key))
	return decision
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
