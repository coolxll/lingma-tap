package bridge

import (
	"context"
	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/proto"
)

// MaxTokensLimit is the maximum tokens allowed to avoid upstream rejection.
const MaxTokensLimit = 16384

// BridgeHandler serves OpenAI-compatible and Anthropic-compatible API endpoints
// that translate requests to the Lingma API.
type BridgeHandler struct {
	client       *LingmaClient
	session      *auth.Session
	recorder     func(*proto.GatewayLog)
	modelMapping map[string]string
	defaultModel string
	Debug        bool
}

func NewBridgeHandler(session *auth.Session, recorder func(*proto.GatewayLog)) *BridgeHandler {
	h := &BridgeHandler{
		client:       NewLingmaClient(session),
		session:      session,
		recorder:     recorder,
		modelMapping: make(map[string]string),
		defaultModel: "dashscope_qmodel",
	}
	return h
}

// SetDebug enables or disables debug logging for the bridge and its client.
func (h *BridgeHandler) SetDebug(debug bool) {
	h.Debug = debug
	if h.client != nil {
		h.client.Debug = debug
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
