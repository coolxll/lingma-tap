package bridge

import "github.com/lynn/lingma-tap/internal/auth"

// BridgeHandler serves OpenAI-compatible and Anthropic-compatible API endpoints
// that translate requests to the Lingma API.
type BridgeHandler struct {
	client  *LingmaClient
	session *auth.Session
}

func NewBridgeHandler(session *auth.Session) *BridgeHandler {
	return &BridgeHandler{
		client:  NewLingmaClient(session),
		session: session,
	}
}
