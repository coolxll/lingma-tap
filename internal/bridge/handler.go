package bridge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/proto"
)

// MaxTokensLimit is the maximum tokens allowed to avoid upstream rejection.
const MaxTokensLimit = 16384

// BridgeHandler serves OpenAI-compatible and Anthropic-compatible API endpoints
// that translate requests to the Lingma API.
type BridgeHandler struct {
	client                  *LingmaClient
	session                 *auth.Session
	recorder                func(*proto.GatewayLog)
	modelMapping            map[string]string
	defaultModel            string
	payloads                func() bool
	thinkingFallback        *oneShotTTLSet
	thinkingFallbackTTL     time.Duration
	thinkingFallbackEnabled bool
	Debug                   bool
}

func NewBridgeHandler(session *auth.Session, recorder func(*proto.GatewayLog)) *BridgeHandler {
	fallbackEnabled, fallbackTTL := loadLingmaThinkingFallbackConfig()
	h := &BridgeHandler{
		client:                  NewLingmaClient(session),
		session:                 session,
		recorder:                recorder,
		modelMapping:            make(map[string]string),
		defaultModel:            DefaultAnthropicModel,
		payloads:                func() bool { return true },
		thinkingFallback:        newOneShotTTLSet(lingmaThinkingFallbackMaxEntries),
		thinkingFallbackTTL:     fallbackTTL,
		thinkingFallbackEnabled: fallbackEnabled,
	}
	return h
}

// SetPayloadLoggingFunc controls whether full request/response payloads are captured for gateway logs.
func (h *BridgeHandler) SetPayloadLoggingFunc(fn func() bool) {
	if fn == nil {
		h.payloads = func() bool { return true }
		return
	}
	h.payloads = fn
}

func (h *BridgeHandler) shouldRecordPayloads() bool {
	if h == nil || h.payloads == nil {
		return true
	}
	return h.payloads()
}

func (h *BridgeHandler) captureRequestBody(body map[string]any) string {
	if !h.shouldRecordPayloads() {
		return ""
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func (h *BridgeHandler) captureResponseBody(log *proto.GatewayLog, resp any) {
	if !h.shouldRecordPayloads() {
		return
	}
	b, _ := json.Marshal(resp)
	log.ResponseBody = string(b)
}

func (h *BridgeHandler) captureResponseBytes(log *proto.GatewayLog, resp []byte) {
	if !h.shouldRecordPayloads() {
		return
	}
	log.ResponseBody = string(resp)
}

// SetDebug enables or disables debug logging for the bridge and its client.
func (h *BridgeHandler) SetDebug(debug bool) {
	h.Debug = debug
	if h.client != nil {
		h.client.Debug = debug
	}
}

func (h *BridgeHandler) SetLingmaHTTP2(enabled bool) {
	if h.client != nil {
		h.client.SetHTTP2Enabled(enabled)
	}
}

// UpdateAnthropicMapping updates the internal model mapping for Anthropic models.
func (h *BridgeHandler) UpdateAnthropicMapping(mapping map[string]string, defaultModel string) {
	h.modelMapping = mapping
	h.defaultModel = defaultModel
}

// GetModels fetches the model list from the Lingma API with friendly names applied.
func (h *BridgeHandler) GetModels() ([]ModelInfo, error) {
	models, err := h.client.FetchModels(context.Background())
	if err != nil {
		return nil, err
	}
	for i := range models {
		models[i].DisplayName = friendlyName(models[i].Key, models[i].DisplayName)
	}
	return models, nil
}
