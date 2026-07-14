package main

import (
	"context"
	crypto_rand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coolxll/lingma-tap/internal/api"
	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/bridge"
	"github.com/coolxll/lingma-tap/internal/encoding"
	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/coolxll/lingma-tap/internal/storage"
)

// Server holds the headless gateway server state and HTTP handlers.
type Server struct {
	DataDir            string
	DB                 *storage.DB
	Debug              bool
	BridgeMu           sync.RWMutex
	BridgeInst         *bridge.BridgeHandler
	Handler            *api.Handler
	GatewayLogging     bool
	PendingOAuthStates *sync.Map

	// Overridable auth functions for testing.
	LoadCredentialsFromBytes func(id, user string) (*auth.Credentials, error)
	SaveCredentialsToDir     func(dataDir, id, user string) error
	ExchangeCallback         func(userID, token, machineID string) (*auth.Credentials, error)
	SaveExchangedCredentials func(creds *auth.Credentials, dataDir string) error
	NewSession               func(creds *auth.Credentials) *auth.Session
}

// NewServer creates a new Server with the given dependencies.
func NewServer(dataDir string, db *storage.DB, debug bool) *Server {
	s := &Server{
		DataDir:                  dataDir,
		DB:                       db,
		Debug:                    debug,
		GatewayLogging:           true,
		PendingOAuthStates:       &sync.Map{},
		LoadCredentialsFromBytes: auth.LoadCredentialsFromBytes,
		SaveCredentialsToDir:     auth.SaveCredentialsToDir,
		ExchangeCallback:         auth.ExchangeCallback,
		SaveExchangedCredentials: auth.SaveExchangedCredentials,
		NewSession:               auth.NewSession,
	}

	// Load gateway logging setting
	if db != nil {
		loggingSetting, _ := db.GetSetting("gateway_logging")
		s.GatewayLogging = loggingSetting != "false"
	}

	s.BridgeInst = s.tryLoadAuth()
	s.Handler = api.NewGatewayHandler(s.BridgeInst)
	return s
}

func (s *Server) tryLoadAuth() *bridge.BridgeHandler {
	authDir := filepath.Join(s.DataDir, "auth")
	idPath := filepath.Join(authDir, "id")
	userPath := filepath.Join(authDir, "user")

	idData, err := os.ReadFile(idPath)
	if err != nil {
		log.Printf("[server] No persisted auth found, waiting for upload via POST /api/auth/upload")
		return nil
	}
	userData, err := os.ReadFile(userPath)
	if err != nil {
		log.Printf("[server] No persisted auth found, waiting for upload via POST /api/auth/upload")
		return nil
	}

	creds, err := s.LoadCredentialsFromBytes(string(idData), string(userData))
	if err != nil {
		log.Printf("[server] Failed to load persisted auth: %v", err)
		return nil
	}

	return s.buildBridge(creds)
}

func (s *Server) buildBridge(creds *auth.Credentials) *bridge.BridgeHandler {
	session := s.NewSession(creds)
	recorder := func(gLog *proto.GatewayLog) {
		snapshot := gatewayLogSnapshot(gLog, s.GatewayLogging)
		if snapshot == nil {
			return
		}
		if s.DB != nil {
			go func(logSnapshot *proto.GatewayLog) {
				if err := s.DB.SaveGatewayLog(logSnapshot); err != nil {
					log.Printf("[server] SaveGatewayLog error: %v", err)
				}
			}(snapshot)
		}
	}
	bh := bridge.NewBridgeHandler(session, recorder)
	if s.Debug {
		bh.SetDebug(true)
	}
	// Inject ResponsesStateStore for multi-turn conversation support
	if s.DB != nil {
		bh.SetResponsesStateStore(storage.NewResponsesStateStore(s.DB))
	}
	s.loadModelMapping(bh)
	log.Printf("[server] Bridge initialized for user %s", creds.Name)
	return bh
}

func gatewayLogSnapshot(l *proto.GatewayLog, includeFullResponse bool) *proto.GatewayLog {
	if l == nil {
		return nil
	}
	cp := *l
	if len(l.SSEEvents) > 0 {
		cp.SSEEvents = append([]proto.SSEEvent(nil), l.SSEEvents...)
	}
	if !includeFullResponse {
		cp.ResponseBody = ""
		cp.SSEEvents = nil
		cp.SSEEventsJSON = ""
	}
	return &cp
}

func (s *Server) loadModelMapping(bh *bridge.BridgeHandler) {
	if s.DB == nil {
		return
	}
	mappingJSON, _ := s.DB.GetSetting("anthropic_model_mapping")
	defaultModel, _ := s.DB.GetSetting("default_anthropic_model")

	fallbackDefaultModel := defaultModel
	if fallbackDefaultModel == "" {
		fallbackDefaultModel = bridge.DefaultAnthropicModel
	}

	if mappingJSON != "" {
		var mapping map[string]string
		if err := json.Unmarshal([]byte(mappingJSON), &mapping); err == nil && len(mapping) > 0 {
			bh.UpdateAnthropicMapping(mapping, fallbackDefaultModel)
			return
		}
	}
	// Fallback defaults
	bh.UpdateAnthropicMapping(bridge.DefaultAnthropicModelMapping(), fallbackDefaultModel)
}

func (s *Server) RegisterManagementRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", s.HandleHealth)
	mux.HandleFunc("/api/auth/status", s.HandleAuthStatus)
	mux.HandleFunc("/api/auth/upload", s.HandleAuthUpload)
	mux.HandleFunc("/api/auth/probe", s.HandleAuthProbe)
	mux.HandleFunc("/api/auth/callback", s.HandleAuthCallback)
	mux.HandleFunc("/api/status", s.HandleServerStatus)
	mux.HandleFunc("/api/gateway/logs", s.HandleGatewayLogs)
}

func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) HandleAuthStatus(w http.ResponseWriter, r *http.Request) {
	s.BridgeMu.RLock()
	bh := s.BridgeInst
	s.BridgeMu.RUnlock()

	authenticated := bh != nil
	writeJSON(w, map[string]interface{}{
		"authenticated": authenticated,
	})
}

func (s *Server) HandleAuthUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(1 << 20); err != nil { // 1MB max
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	idFile, _, err := r.FormFile("id")
	if err != nil {
		http.Error(w, "Missing 'id' file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer idFile.Close()

	userFile, _, err := r.FormFile("user")
	if err != nil {
		http.Error(w, "Missing 'user' file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer userFile.Close()

	idBytes, err := io.ReadAll(idFile)
	if err != nil {
		http.Error(w, "Failed to read id file", http.StatusBadRequest)
		return
	}
	userBytes, err := io.ReadAll(userFile)
	if err != nil {
		http.Error(w, "Failed to read user file", http.StatusBadRequest)
		return
	}

	creds, err := s.LoadCredentialsFromBytes(string(idBytes), string(userBytes))
	if err != nil {
		http.Error(w, "Invalid credentials: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Persist to disk
	if err := s.SaveCredentialsToDir(s.DataDir, string(idBytes), string(userBytes)); err != nil {
		log.Printf("[server] Warning: failed to persist auth files: %v", err)
	}

	bh := s.buildBridge(creds)

	s.BridgeMu.Lock()
	s.BridgeInst = bh
	s.BridgeMu.Unlock()
	s.Handler.SetBridge(bh)

	writeJSON(w, map[string]interface{}{
		"ok":   true,
		"user": creds.Name,
	})
}

func (s *Server) HandleAuthProbe(w http.ResponseWriter, r *http.Request) {
	// Generate random state for CSRF protection
	state := fmt.Sprintf("probe-%x", randomBytes(8))
	s.PendingOAuthStates.Store(state, time.Now())

	// Start temporary callback server on random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		http.Error(w, "Failed to start callback server: "+err.Error(), 500)
		return
	}
	callbackPort := listener.Addr().(*net.TCPAddr).Port
	log.Printf("[probe] Temporary callback server on port %d (state=%s)", callbackPort, state)

	// Callback handler: log everything
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(cw http.ResponseWriter, cr *http.Request) {
		log.Printf("[probe] ===== CALLBACK RECEIVED =====")
		log.Printf("[probe] Method: %s", cr.Method)
		log.Printf("[probe] URL: %s", cr.URL.String())
		log.Printf("[probe] Path: %s", cr.URL.Path)
		log.Printf("[probe] Query: %v", cr.URL.Query())
		log.Printf("[probe] Headers:")
		for k, v := range cr.Header {
			log.Printf("[probe]   %s: %s", k, strings.Join(v, ", "))
		}
		if cr.Body != nil {
			body, _ := io.ReadAll(cr.Body)
			if len(body) > 0 {
				log.Printf("[probe] Body (%d bytes): %s", len(body), string(body))
			}
		}
		log.Printf("[probe] ===== END CALLBACK =====")

		cw.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(cw, "<h1>OAuth Probe Received!</h1><p>Check server logs for details.</p><pre>State: %s\nPath: %s\nQuery: %s</pre>",
			state, cr.URL.Path, cr.URL.RawQuery)
	})

	probeServer := &http.Server{Handler: mux}
	go func() {
		if err := probeServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("[probe] Callback server error: %v", err)
		}
	}()

	// Auto-close after 60 seconds
	go func() {
		time.Sleep(60 * time.Second)
		log.Printf("[probe] Timeout, closing callback server")
		probeServer.Close()
		s.PendingOAuthStates.Delete(state)
	}()

	// Construct login URL
	loginURL := fmt.Sprintf(
		"https://signin.aliyun.com/dongfangfuli.onaliyun.com/login.htm?callback=https%%3A%%2F%%2Fdevops.aliyun.com%%2Flingma%%2Flogin%%3Fstate%%3D%s%%26port%%3D%d&oauth_callback=https%%3A%%2F%%2Fdevops.aliyun.com%%2Flingma%%2Flogin%%3Fstate%%3D%s%%26port%%3D%d",
		state, callbackPort, state, callbackPort,
	)

	log.Printf("[probe] Login URL: %s", loginURL)

	writeJSON(w, map[string]interface{}{
		"login_url":     loginURL,
		"callback_port": callbackPort,
		"state":         state,
		"expires_in":    60,
	})
}

func (s *Server) HandleAuthCallback(w http.ResponseWriter, r *http.Request) {
	stateParam := r.URL.Query().Get("state")
	if stateParam == "" {
		http.Error(w, "Missing state parameter", http.StatusBadRequest)
		return
	}
	if _, ok := s.PendingOAuthStates.LoadAndDelete(stateParam); !ok {
		http.Error(w, "Invalid or expired state parameter", http.StatusForbidden)
		return
	}

	authParam := r.URL.Query().Get("auth")
	tokenParam := r.URL.Query().Get("token")

	if authParam == "" || tokenParam == "" {
		http.Error(w, "Missing auth or token parameters", http.StatusBadRequest)
		return
	}

	// 1. Decode parameters
	decodedAuth, err := encoding.Decode(authParam)
	if err != nil {
		http.Error(w, "Failed to decode auth param: "+err.Error(), http.StatusBadRequest)
		return
	}
	decodedToken, err := encoding.Decode(tokenParam)
	if err != nil {
		http.Error(w, "Failed to decode token param: "+err.Error(), http.StatusBadRequest)
		return
	}

	var authInfo struct {
		UserId string `json:"userId"`
	}
	if err := json.Unmarshal(decodedAuth, &authInfo); err != nil {
		http.Error(w, "Failed to parse auth info: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 2. Perform automated exchange (handshake)
	machineID := s.GetOrGenerateMachineID()
	creds, err := s.ExchangeCallback(authInfo.UserId, string(decodedToken), machineID)
	if err != nil {
		log.Printf("[server] Exchange error: %v", err)
		http.Error(w, "Exchange failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3. Persist credentials
	if err := s.SaveExchangedCredentials(creds, s.DataDir); err != nil {
		log.Printf("[server] Failed to persist credentials: %v", err)
		// Continue anyway as we have it in memory
	}

	// 4. Hot-reload bridge
	bh := s.buildBridge(creds)

	s.BridgeMu.Lock()
	s.BridgeInst = bh
	s.BridgeMu.Unlock()
	s.Handler.SetBridge(bh)

	log.Printf("[server] Successfully exchanged credentials for user %s (%s)", creds.Name, creds.UID)

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, "<h1>Authentication Successful!</h1><p>Welcome, %s. You can now use the Lingma Gateway.</p>", creds.Name)
}

// GetOrGenerateMachineID returns the existing machine ID or generates a new one.
func (s *Server) GetOrGenerateMachineID() string {
	idPath := filepath.Join(s.DataDir, "auth", "id")
	if data, err := os.ReadFile(idPath); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}

	// Generate a new UUID-like machine ID
	newID := fmt.Sprintf("%x-%x-%x-%x-%x",
		randomBytes(4), randomBytes(2), randomBytes(2), randomBytes(2), randomBytes(6))

	// Ensure auth dir exists
	os.MkdirAll(filepath.Join(s.DataDir, "auth"), 0755)
	os.WriteFile(idPath, []byte(newID), 0600)

	return newID
}

func (s *Server) HandleServerStatus(w http.ResponseWriter, r *http.Request) {
	s.BridgeMu.RLock()
	bh := s.BridgeInst
	s.BridgeMu.RUnlock()

	status := map[string]interface{}{
		"authenticated":   bh != nil,
		"gateway_logging": s.GatewayLogging,
	}
	if s.DB != nil {
		status["stats"] = s.DB.Stats()
	}
	writeJSON(w, status)
}

func (s *Server) HandleGatewayLogs(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeJSON(w, []interface{}{})
		return
	}

	limit := 100
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	logs, err := s.DB.RecentGatewayLogs(limit, offset)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, logs)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	crypto_rand.Read(b)
	return b
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

var (
	dataDir            string
	handler            *api.Handler
	bridgeMu           sync.RWMutex
	bridgeInst         *bridge.BridgeHandler
	db                 *storage.DB
	gatewayLogging     bool
	pendingOAuthStates sync.Map
)

func main() {
	// Config from env
	port := envOrDefault("GATEWAY_PORT", "9090")
	dataDir = envOrDefault("DATA_DIR", "/data")
	debug := os.Getenv("GATEWAY_DEBUG") == "1"

	os.MkdirAll(dataDir, 0755)

	// Logging
	logFile, err := os.OpenFile(filepath.Join(dataDir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	}
	log.Printf("[server] Starting Lingma Gateway on :%s (data=%s, debug=%v)", port, dataDir, debug)

	// SQLite
	dbPath := filepath.Join(dataDir, "lingma-tap.db")
	db, err = storage.Open(dbPath)
	if err != nil {
		log.Fatalf("[server] SQLite open error: %v", err)
	}
	defer db.Close()

	// Load gateway logging setting
	loggingSetting, _ := db.GetSetting("gateway_logging")
	gatewayLogging = loggingSetting != "false"

	// Create server instance
	server := NewServer(dataDir, db, debug)
	handler = server.Handler

	// HTTP mux
	mux := http.NewServeMux()
	handler.RegisterGatewayRoutes(mux)
	server.RegisterManagementRoutes(mux)

	listenAddr := envOrDefault("LISTEN_ADDR", "0.0.0.0")
	addr := listenAddr + ":" + port
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSE streaming needs no write timeout
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("[server] Received %s, shutting down...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("[server] Listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[server] ListenAndServe error: %v", err)
	}
	log.Println("[server] Stopped")
}
