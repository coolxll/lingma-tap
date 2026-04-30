package bridge

import (
	"context"
	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/proto"
)

// BridgeHandler serves OpenAI-compatible and Anthropic-compatible API endpoints
// that translate requests to the Lingma API.
type BridgeHandler struct {
	client   *LingmaClient
	session  *auth.Session
	recorder func(*proto.GatewayLog)
}

func NewBridgeHandler(session *auth.Session, recorder func(*proto.GatewayLog)) *BridgeHandler {
	return &BridgeHandler{
		client:   NewLingmaClient(session),
		session:  session,
		recorder: recorder,
	}
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
