package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/coolxll/lingma-tap/internal/bridge"
	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/gorilla/websocket"
)

type RecordStore interface {
	RecentRecords(limit int) ([]proto.Record, error)
	ClearTraffic() error
	Stats() interface{}
}

type Handler struct {
	hub    *Hub
	store  RecordStore
	bridge BridgeHandler
}

type BridgeHandler interface {
	HandleModels(w http.ResponseWriter, r *http.Request)
	HandleOpenAIChat(w http.ResponseWriter, r *http.Request)
	HandleOpenAIResponses(w http.ResponseWriter, r *http.Request)
	HandleAnthropicMessages(w http.ResponseWriter, r *http.Request)
	GetModels() ([]bridge.ModelInfo, error)
}

func NewHandler(hub *Hub, store RecordStore, bridge BridgeHandler) *Handler {
	return &Handler{hub: hub, store: store, bridge: bridge}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (h *Handler) RegisterInternalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/records", h.HandleWebSocket)
	mux.HandleFunc("/api/records", h.handleRecords)
	mux.HandleFunc("/api/status", h.handleStatus)
}

func (h *Handler) RegisterGatewayRoutes(mux *http.ServeMux) {
	// Bridge endpoints (OpenAI / Anthropic compatible)
	if h.bridge != nil {
		mux.HandleFunc("/v1/models", corsMiddleware(h.bridge.HandleModels))
		mux.HandleFunc("/v1/models/", corsMiddleware(h.bridge.HandleModels))
		mux.HandleFunc("/v1/chat/completions", corsMiddleware(h.bridge.HandleOpenAIChat))
		mux.HandleFunc("/v1/responses", corsMiddleware(h.bridge.HandleOpenAIResponses))
		mux.HandleFunc("/v1/messages", corsMiddleware(h.bridge.HandleAnthropicMessages))
	}
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	clientID := r.URL.Query().Get("client_id")
	remoteHost := r.RemoteAddr
	client := NewClient(h.hub, conn, clientID, remoteHost)
	h.hub.register <- client

	go client.WritePump()
	client.ReadPump()
}

func (h *Handler) handleRecords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	switch r.Method {
	case http.MethodGet:
		limit := 100
		if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
		records, err := h.store.RecentRecords(limit)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, records)
	case http.MethodDelete:
		if err := h.store.ClearTraffic(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case http.MethodOptions:
		w.Header().Set("Access-Control-Allow-Methods", "GET, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, map[string]interface{}{
		"ws_clients": h.hub.ClientCount(),
		"stats":      h.store.Stats(),
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version, openai-beta")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}
