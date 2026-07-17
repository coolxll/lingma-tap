package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

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
	hub      *Hub
	store    RecordStore
	bridgeMu sync.RWMutex
	bridge   BridgeHandler
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

// NewGatewayHandler creates a Handler for server mode with only the bridge.
// hub and store are nil — gateway routes work independently.
func NewGatewayHandler(bridge BridgeHandler) *Handler {
	return &Handler{bridge: bridge}
}

// SetBridge hot-reloads the bridge handler (e.g. after auth file upload).
func (h *Handler) SetBridge(bridge BridgeHandler) {
	h.bridgeMu.Lock()
	defer h.bridgeMu.Unlock()
	h.bridge = bridge
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (h *Handler) RegisterInternalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/records", h.HandleWebSocket)
	mux.HandleFunc("/api/records", h.handleRecords)
	mux.HandleFunc("/api/records/", h.handleRecordBody)
	mux.HandleFunc("/api/artifacts/", h.handleArtifact)
	mux.HandleFunc("/api/status", h.handleStatus)
}

type bodyStore interface {
	GetRecordBody(id int64) ([]byte, string, bool, error)
	GetRecordBodyByKey(session string, index int) ([]byte, string, bool, error)
}

// decodedBodyStore is optional so lightweight gateway/test stores can keep
// implementing the raw-body contract while the SQLite store serves decoded
// previews for encoded Lingma requests.
type decodedBodyStore interface {
	GetRecordBodyDecoded(id int64) ([]byte, string, bool, error)
	GetRecordBodyDecodedByKey(session string, index int) ([]byte, string, bool, error)
}

type artifactStore interface {
	GetArtifactBody(id int64) ([]byte, string, error)
}

func (h *Handler) handleRecordBody(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !authorizeLocalAssetOrigin(w, r) {
		return
	}
	store, ok := h.store.(bodyStore)
	if !ok {
		http.Error(w, "body storage unavailable", http.StatusNotImplemented)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/records/")
	path = strings.TrimSuffix(path, "/body")
	id, err := strconv.ParseInt(strings.Trim(path, "/"), 10, 64)
	var body []byte
	var mime string
	var truncated bool
	decoded := r.URL.Query().Get("view") == "decoded"
	decodedStore, hasDecodedStore := h.store.(decodedBodyStore)
	if err == nil && id > 0 {
		if decoded && hasDecodedStore {
			body, mime, truncated, err = decodedStore.GetRecordBodyDecoded(id)
		} else {
			body, mime, truncated, err = store.GetRecordBody(id)
		}
	} else {
		index, indexErr := strconv.Atoi(r.URL.Query().Get("index"))
		session := strings.TrimSpace(r.URL.Query().Get("session"))
		if indexErr != nil || session == "" {
			http.Error(w, "invalid record id or key", http.StatusBadRequest)
			return
		}
		if decoded && hasDecodedStore {
			body, mime, truncated, err = decodedStore.GetRecordBodyDecodedByKey(session, index)
		} else {
			body, mime, truncated, err = store.GetRecordBodyByKey(session, index)
		}
	}
	if err != nil {
		http.Error(w, "record body not found", http.StatusNotFound)
		return
	}
	// This endpoint is intentionally local/same-origin; unlike the metadata
	// endpoint it must not expose captured images to arbitrary web origins.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", mimeOrOctetStream(mime))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if truncated {
		w.Header().Set("X-Lingma-Tap-Truncated", "true")
	}
	_, _ = w.Write(body)
}

func mimeOrOctetStream(mime string) string {
	if strings.TrimSpace(mime) == "" {
		return "application/octet-stream"
	}
	return mime
}

func (h *Handler) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !authorizeLocalAssetOrigin(w, r) {
		return
	}
	store, ok := h.store.(artifactStore)
	if !ok {
		http.Error(w, "artifact storage unavailable", http.StatusNotImplemented)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/artifacts/")
	id, err := strconv.ParseInt(strings.Trim(path, "/"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid artifact id", http.StatusBadRequest)
		return
	}
	body, mime, err := store.GetArtifactBody(id)
	if err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", mimeOrOctetStream(mime))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

func authorizeLocalAssetOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Same-origin requests and local CLI diagnostics do not send Origin.
		return true
	}
	allowed := map[string]struct{}{
		"http://localhost:5173":   {},
		"http://127.0.0.1:5173":   {},
		"http://localhost:9091":   {},
		"http://127.0.0.1:9091":   {},
		"http://wails.localhost":  {},
		"https://wails.localhost": {},
		"wails://wails":           {},
	}
	if _, ok := allowed[origin]; !ok {
		http.Error(w, "asset origin not allowed", http.StatusForbidden)
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
	return true
}

func (h *Handler) RegisterGatewayRoutes(mux *http.ServeMux) {
	// Keep routes available before login so a bridge can be installed later
	// without restarting the desktop gateway.
	mux.HandleFunc("/v1/models", corsMiddleware(h.handleModels))
	mux.HandleFunc("/v1/models/", corsMiddleware(h.handleModels))
	mux.HandleFunc("/v1/chat/completions", corsMiddleware(h.handleOpenAIChat))
	mux.HandleFunc("/v1/responses", corsMiddleware(h.handleOpenAIResponses))
	mux.HandleFunc("/v1/messages", corsMiddleware(h.handleAnthropicMessages))
}

func (h *Handler) isBridgeNil() bool {
	return isNilBridge(h.currentBridge())
}

func isNilBridge(handler BridgeHandler) bool {
	if handler == nil {
		return true
	}
	// Check if the interface contains a nil pointer
	if bh, ok := handler.(*bridge.BridgeHandler); ok {
		return bh == nil
	}
	return false
}

func (h *Handler) currentBridge() BridgeHandler {
	h.bridgeMu.RLock()
	defer h.bridgeMu.RUnlock()
	return h.bridge
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	if bridge := h.currentBridge(); !isNilBridge(bridge) {
		bridge.HandleModels(w, r)
		return
	}
	bridgeUnavailable(w)
}

func (h *Handler) handleOpenAIChat(w http.ResponseWriter, r *http.Request) {
	if bridge := h.currentBridge(); !isNilBridge(bridge) {
		bridge.HandleOpenAIChat(w, r)
		return
	}
	bridgeUnavailable(w)
}

func (h *Handler) handleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	if bridge := h.currentBridge(); !isNilBridge(bridge) {
		bridge.HandleOpenAIResponses(w, r)
		return
	}
	bridgeUnavailable(w)
}

func (h *Handler) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if bridge := h.currentBridge(); !isNilBridge(bridge) {
		bridge.HandleAnthropicMessages(w, r)
		return
	}
	bridgeUnavailable(w)
}

func bridgeUnavailable(w http.ResponseWriter) {
	http.Error(w, "Lingma authentication is not available", http.StatusServiceUnavailable)
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
