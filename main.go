package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/coolxll/lingma-tap/internal/api"
	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/bridge"
	"github.com/coolxll/lingma-tap/internal/ca"
	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/coolxll/lingma-tap/internal/proxy"
	"github.com/coolxll/lingma-tap/internal/storage"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Version is injected at build time via -ldflags for release builds.
// Example: go build -ldflags "-X main.Version=$(git describe --tags --always --dirty)"
var Version = "dev"

//go:embed all:web/dist
var webAssets embed.FS

type App struct {
	ctx                context.Context
	mu                 sync.Mutex
	dataDir            string
	ca                 *ca.CA
	db                 *storage.DB
	sink               *storage.AsyncSink
	hub                *api.Hub
	proxy              *proxy.Server
	bridgeHandlerField *bridge.BridgeHandler
	managementHandler  *api.Handler
	gatewayHandler     *api.Handler
	oauthLogin         *auth.OAuthLogin
	authUser           string
	authExpireTime     int64
	openURL            func(string)
	apiLn              net.Listener
	proxyRunning       bool
	proxyPort          int
	gatewayRunning     bool
	gatewayServer      *http.Server
	gatewayLogging     bool
	proxyLogging       bool
	lingmaHTTP2        bool
}

func NewApp() *App {
	a := &App{
		gatewayLogging: true,
		proxyLogging:   true,
		oauthLogin:     auth.NewOAuthLogin(),
	}
	a.openURL = func(url string) {
		runtime.BrowserOpenURL(a.ctx, url)
	}
	return a
}

func gatewayLogSnapshot(l *proto.GatewayLog, includePayloads bool) *proto.GatewayLog {
	if l == nil {
		return nil
	}
	cp := *l
	if !includePayloads {
		cp.RequestBody = ""
		cp.ResponseBody = ""
		cp.SSEEvents = nil
		cp.SSEEventsJSON = ""
		return &cp
	}
	if len(l.SSEEvents) > 0 {
		cp.SSEEvents = append([]proto.SSEEvent(nil), l.SSEEvents...)
	}
	return &cp
}

func convertGatewayLogToRecord(l *proto.GatewayLog) *proto.Record {
	return &proto.Record{
		ID:                 l.ID,
		Ts:                 l.Ts,
		Session:            l.Session,
		Source:             "gateway",
		Method:             l.Method,
		Path:               l.Path,
		EndpointType:       "chat",
		ReqBody:            l.RequestBody,
		RespBody:           l.ResponseBody,
		Status:             l.Status,
		IsSSE:              l.IsSSE,
		SSEEvents:          l.SSEEvents,
		Error:              l.Error,
		Model:              l.Model,
		InputTokens:        l.InputTokens,
		OutputTokens:       l.OutputTokens,
		CachedTokens:       l.CachedTokens,
		ReasoningTokens:    l.ReasoningTokens,
		TotalTokens:        l.TotalTokens,
		TTFT:               l.TTFT,
		UpstreamAttempts:   l.UpstreamAttempts,
		RecoveryApplied:    l.RecoveryApplied,
		UpstreamErrorClass: l.UpstreamErrorClass,
		FirstActionableMS:  l.FirstActionableMS,
		ReasoningOnlyBytes: l.ReasoningOnlyBytes,
		RequestedProfile:   l.RequestedProfile,
		EffectiveProfile:   l.EffectiveProfile,
		Latency:            l.Latency,
		FinishReason:       l.FinishReason,
		ReqHeaders:         make(map[string]string),
		RespHeaders:        make(map[string]string),
	}
}

func (a *App) loadDesktopCredentials(dataDir string) (*auth.Credentials, error) {
	creds, err := auth.LoadCredentialsFromDir(filepath.Join(dataDir, "auth"))
	if err == nil {
		return creds, nil
	}
	return auth.LoadCredentials()
}

func (a *App) buildBridge(creds *auth.Credentials) *bridge.BridgeHandler {
	session := auth.NewSession(creds)
	handler := bridge.NewBridgeHandler(session, func(gLog *proto.GatewayLog) {
		a.mu.Lock()
		includePayloads := a.gatewayLogging
		a.mu.Unlock()

		snapshot := gatewayLogSnapshot(gLog, includePayloads)
		if snapshot == nil {
			return
		}
		if a.db != nil {
			go func(logSnapshot *proto.GatewayLog) {
				if err := a.db.SaveGatewayLog(logSnapshot); err != nil {
					log.Printf("[app] SaveGatewayLog error: %v", err)
				}
			}(snapshot)
		}
		if a.hub != nil {
			rec := convertGatewayLogToRecord(snapshot)
			a.hub.Broadcast(rec)
		}
	})
	handler.SetPayloadLoggingFunc(func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.gatewayLogging
	})
	if os.Getenv("GATEWAY_DEBUG") == "1" {
		handler.SetDebug(true)
	}
	a.mu.Lock()
	lingmaHTTP2 := a.lingmaHTTP2
	a.mu.Unlock()
	handler.SetLingmaHTTP2(lingmaHTTP2)

	// Inject ResponsesStateStore for multi-turn conversation support
	if a.db != nil {
		handler.SetResponsesStateStore(storage.NewResponsesStateStore(a.db))
	}

	mappingJSON := ""
	defaultModel := ""
	if a.db != nil {
		mappingJSON, _ = a.db.GetSetting("anthropic_model_mapping")
		defaultModel, _ = a.db.GetSetting("default_anthropic_model")
	}
	fallbackDefaultModel := defaultModel
	if fallbackDefaultModel == "" {
		fallbackDefaultModel = bridge.DefaultAnthropicModel
	}
	if mappingJSON != "" {
		var mapping map[string]string
		if err := json.Unmarshal([]byte(mappingJSON), &mapping); err == nil && len(mapping) > 0 {
			handler.UpdateAnthropicMapping(mapping, fallbackDefaultModel)
			return handler
		}
	}
	handler.UpdateAnthropicMapping(bridge.DefaultAnthropicModelMapping(), fallbackDefaultModel)
	return handler
}

func (a *App) installCredentials(creds *auth.Credentials) {
	bridgeHandler := a.buildBridge(creds)

	a.mu.Lock()
	a.bridgeHandlerField = bridgeHandler
	a.authUser = creds.Name
	a.authExpireTime = creds.ExpireTime
	managementHandler := a.managementHandler
	gatewayHandler := a.gatewayHandler
	a.mu.Unlock()

	if managementHandler != nil {
		managementHandler.SetBridge(bridgeHandler)
	}
	if gatewayHandler != nil {
		gatewayHandler.SetBridge(bridgeHandler)
	}
	log.Printf("[app] Bridge initialized for user %s", creds.Name)
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".lingma-tap")
	os.MkdirAll(dataDir, 0755)
	a.dataDir = dataDir

	// Persist logs to file
	logFile, err := os.OpenFile(filepath.Join(dataDir, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
		log.Println("--- App Started ---")
	}

	// Initialize status bar / system tray before services that may fail.
	startTray(a)

	// Initialize CA
	c, err := ca.New(dataDir)
	if err != nil {
		log.Printf("[app] CA init error: %v", err)
		return
	}
	a.ca = c

	// Initialize SQLite
	dbPath := filepath.Join(dataDir, "lingma-tap.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Printf("[app] SQLite open error: %v", err)
		return
	}
	a.db = db
	a.sink = storage.NewAsyncSink(db, 10000)

	lingmaHTTP2Setting, _ := a.db.GetSetting("lingma_http2")
	if lingmaHTTP2Setting == "" {
		a.lingmaHTTP2 = bridge.DefaultLingmaHTTP2Enabled()
	} else {
		a.lingmaHTTP2 = lingmaHTTP2Setting == "true"
	}

	// Initialize WebSocket Hub
	a.hub = api.NewHub()
	go a.hub.Run()
	a.sink.SetOnSaved(func(rec *proto.Record) {
		if a.hub != nil {
			a.hub.Broadcast(rec)
		}
	})

	// Start API server (WebSocket + REST + Bridge)
	creds, err := a.loadDesktopCredentials(dataDir)
	if err != nil {
		log.Printf("[app] LocalAuth not available (bridge disabled): %v", err)
	} else {
		a.installCredentials(creds)
	}

	handler := api.NewHandler(a.hub, a, a.bridgeHandlerField)
	a.managementHandler = handler
	mux := http.NewServeMux()
	handler.RegisterInternalRoutes(mux)

	a.apiLn, err = net.Listen("tcp", "127.0.0.1:9091")
	if err != nil {
		log.Printf("[app] API listen error: %v", err)
		return
	}
	go http.Serve(a.apiLn, mux)
	log.Printf("[app] Management API server on %s", a.apiLn.Addr())

	loggingSetting, _ := a.db.GetSetting("gateway_logging")
	if loggingSetting == "false" {
		a.gatewayLogging = false
	} else {
		a.gatewayLogging = true
	}

	// Proxy logging defaults to true (independent of gateway logging)
	proxyLoggingSetting, _ := a.db.GetSetting("proxy_logging")
	if proxyLoggingSetting == "false" {
		a.proxyLogging = false
	} else {
		a.proxyLogging = true
	}

	a.proxy = proxy.NewServer(a.ca, func(rec *proto.Record) {
		rec.Source = "proxy"
		a.mu.Lock()
		logging := a.proxyLogging
		a.mu.Unlock()

		if logging {
			if a.sink != nil {
				a.sink.SaveRecord(rec)
			}
			if a.hub != nil {
				a.hub.Broadcast(rec)
			}
		}
	})

	log.Printf("[app] CA cert: %s", a.ca.CertPath())

	// Auto-start Proxy on default port 9528
	go func() {
		time.Sleep(500 * time.Millisecond) // Give Wails a moment to settle
		if err := a.StartProxy(9528); err != nil {
			log.Printf("[app] Auto-start Proxy error: %v", err)
		} else {
			log.Printf("[app] Auto-started Proxy on port 9528")
		}
	}()

	// Auto-start AI Gateway on default port 9090
	go func() {
		time.Sleep(600 * time.Millisecond)
		if err := a.StartGateway(9090, "127.0.0.1"); err != nil {
			log.Printf("[app] Auto-start Gateway error: %v", err)
		} else {
			log.Printf("[app] Auto-started AI Gateway on port 9090")
		}
	}()
}

func (a *App) shutdown(ctx context.Context) {
	stopTray()
	if a.oauthLogin != nil {
		a.oauthLogin.Close()
	}
	if a.hub != nil {
		a.hub.Stop()
	}
	if a.proxy != nil {
		a.proxy.Stop()
	}
	if a.apiLn != nil {
		a.apiLn.Close()
	}
	if a.sink != nil {
		a.sink.Close()
	}
	if a.db != nil {
		a.db.Close()
	}
}

// StartProxy starts the MITM proxy on the given port.
func (a *App) StartProxy(port int) error {
	if a.proxy == nil {
		return fmt.Errorf("proxy not initialized")
	}
	return a.proxy.Start(port)
}

// StopProxy stops the MITM proxy.
func (a *App) StopProxy() {
	if a.proxy != nil {
		a.proxy.Stop()
	}
}

// GetNetworkInterfaces returns available network interfaces for binding.
// Detects Tailscale interfaces and returns their IPs.
func (a *App) GetNetworkInterfaces() []map[string]string {
	var interfaces []map[string]string

	// Always include localhost
	interfaces = append(interfaces, map[string]string{
		"name": "Localhost",
		"addr": "127.0.0.1",
		"type": "loopback",
	})

	// Scan network interfaces for Tailscale
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("[app] Failed to list interfaces: %v", err)
		return interfaces
	}

	for _, iface := range ifaces {
		// Skip down or loopback interfaces
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			// Check if this is a Tailscale IP (100.64.0.0/10 CGNAT range)
			if ip4 := ip.To4(); ip4 != nil {
				if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
					interfaces = append(interfaces, map[string]string{
						"name": fmt.Sprintf("Tailscale (%s)", iface.Name),
						"addr": ip4.String(),
						"type": "tailscale",
					})
				}
			}
		}
	}

	// Add "All interfaces" option
	interfaces = append(interfaces, map[string]string{
		"name": "All Interfaces",
		"addr": "0.0.0.0",
		"type": "all",
	})

	return interfaces
}

// StartGateway starts the AI Gateway.
func (a *App) StartGateway(port int, listenAddr string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Validate listenAddr before closing existing server
	if listenAddr == "" {
		listenAddr = "127.0.0.1"
	}

	validAddrs := map[string]bool{
		"127.0.0.1": true,
		"0.0.0.0":   true,
	}

	// Also allow any detected Tailscale IPs
	ifaces := a.GetNetworkInterfaces()
	for _, iface := range ifaces {
		if iface["type"] == "tailscale" {
			validAddrs[iface["addr"]] = true
		}
	}

	if !validAddrs[listenAddr] {
		return fmt.Errorf("invalid listen address: %s (allowed: 127.0.0.1, 0.0.0.0, or Tailscale IP)", listenAddr)
	}

	// Now safe to close existing server
	if a.gatewayServer != nil {
		a.gatewayServer.Close()
	}

	addr := fmt.Sprintf("%s:%d", listenAddr, port)
	handler := api.NewHandler(a.hub, a, a.bridgeHandlerField)
	a.gatewayHandler = handler
	mux := http.NewServeMux()
	handler.RegisterGatewayRoutes(mux)

	a.gatewayServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("[app] AI Gateway starting on %s", addr)
		if err := a.gatewayServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[app] AI Gateway error: %v", err)
		}
	}()

	return nil
}

// StopGateway stops the AI Gateway.
func (a *App) StopGateway() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.gatewayServer != nil {
		log.Printf("[app] Stopping AI Gateway...")
		a.gatewayServer.Close()
		a.gatewayServer = nil
		a.gatewayHandler = nil
	}
}

// GetRecords returns recent proxy traffic records, skipping the first `offset` records.
func (a *App) GetRecords(limit int, offset int) []proto.Record {
	if a.db == nil {
		return nil
	}
	if limit <= 0 {
		limit = 500
	}
	records, _ := a.db.RecentRecords(limit, offset)
	return records
}

// GetRecordsByType returns recent proxy traffic records filtered by endpoint type.
func (a *App) GetRecordsByType(limit int, offset int, recordType string) []proto.Record {
	if a.db == nil {
		return nil
	}
	if limit <= 0 {
		limit = 500
	}
	records, _ := a.db.RecentRecordsByType(limit, offset, recordType)
	return records
}

// GetRecordBody serves captured bytes through the local management API without
// embedding them in Wails record/list payloads.
func (a *App) GetRecordBody(id int64) ([]byte, string, bool, error) {
	if a.db == nil {
		return nil, "", false, fmt.Errorf("database unavailable")
	}
	return a.db.GetRecordBody(id)
}

func (a *App) GetRecordBodyByKey(session string, index int) ([]byte, string, bool, error) {
	if a.db == nil {
		return nil, "", false, fmt.Errorf("database unavailable")
	}
	return a.db.GetRecordBodyByKey(session, index)
}

func (a *App) GetRecordBodyDecoded(id int64) ([]byte, string, bool, error) {
	if a.db == nil {
		return nil, "", false, fmt.Errorf("database unavailable")
	}
	return a.db.GetRecordBodyDecoded(id)
}

func (a *App) GetRecordBodyDecodedByKey(session string, index int) ([]byte, string, bool, error) {
	if a.db == nil {
		return nil, "", false, fmt.Errorf("database unavailable")
	}
	return a.db.GetRecordBodyDecodedByKey(session, index)
}

// GetGatewayLogs returns recent AI Gateway logs, skipping the first `offset` records.
func (a *App) GetGatewayLogs(limit int, offset int) []proto.GatewayLog {
	if a.db == nil {
		return nil
	}
	if limit <= 0 {
		limit = 500
	}
	logs, _ := a.db.RecentGatewayLogs(limit, offset)
	return logs
}

// GetGatewayStats returns aggregate AI Gateway stats for the selected UI time range.
func (a *App) GetGatewayStats(timeRange string, filter string) proto.GatewayLogStats {
	if a.db == nil {
		return proto.GatewayLogStats{}
	}

	now := time.Now()
	since := ""
	switch timeRange {
	case "1h":
		since = now.Add(-time.Hour).Format(time.RFC3339Nano)
	case "today":
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		since = start.Format(time.RFC3339Nano)
	case "7d":
		since = now.Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)
	case "30d":
		since = now.Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
	}

	stats, err := a.db.GatewayLogStats(since, filter)
	if err != nil {
		log.Printf("[app] GatewayLogStats error: %v", err)
		return proto.GatewayLogStats{}
	}
	return stats
}

// ClearRecords clears all traffic data.
func (a *App) ClearRecords() error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return a.db.ClearTraffic()
}

// ClearProxyRecords clears all proxy records.
func (a *App) ClearProxyRecords() error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return a.db.ClearProxyRecords()
}

// ClearGatewayLogs clears all gateway logs.
func (a *App) ClearGatewayLogs() error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return a.db.ClearGatewayLogs()
}

// ClearRecordsBefore clears traffic data older than the specified number of days.
// Returns the number of deleted records.
func (a *App) ClearRecordsBefore(days int) (int, error) {
	if a.db == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	cutoff := time.Now().AddDate(0, 0, -days).Format(time.RFC3339)
	return a.db.ClearTrafficBefore(cutoff)
}

// GetCACertPath returns the CA certificate file path.
func (a *App) GetCACertPath() string {
	if a.ca == nil {
		return ""
	}
	return a.ca.CertPath()
}

// RevealCACert opens the directory containing the CA certificate in the
// platform's file manager (Finder on macOS, Explorer on Windows) so the user
// can drag it into the system trust store.
func (a *App) RevealCACert() error {
	if a.ca == nil {
		return fmt.Errorf("CA is not initialized")
	}
	certPath := a.ca.CertPath()
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("CA certificate not found: %w", err)
	}

	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("/usr/bin/open", "-R", certPath)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+certPath)
	case "linux":
		cmd = exec.Command("xdg-open", filepath.Dir(certPath))
	default:
		return fmt.Errorf("reveal CA certificate is not supported on %s", goruntime.GOOS)
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		if len(output) > 0 {
			return fmt.Errorf("reveal CA certificate: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("reveal CA certificate: %w", err)
	}
	return nil
}

// SetLogging controls whether full AI Gateway request/response payloads are persisted.
// Gateway metadata and token usage are always recorded.
func (a *App) SetLogging(enabled bool) {
	a.mu.Lock()
	a.gatewayLogging = enabled
	a.mu.Unlock()

	if a.db != nil {
		val := "false"
		if enabled {
			val = "true"
		}
		a.db.SaveSetting("gateway_logging", val)
	}
}

// SetProxyLogging enables or disables proxy traffic logging.
func (a *App) SetProxyLogging(enabled bool) {
	a.mu.Lock()
	a.proxyLogging = enabled
	a.mu.Unlock()

	if a.db != nil {
		val := "false"
		if enabled {
			val = "true"
		}
		a.db.SaveSetting("proxy_logging", val)
	}
}

// SetLingmaHTTP2 controls whether Lingma upstream requests may use HTTP/2.
func (a *App) SetLingmaHTTP2(enabled bool) {
	a.mu.Lock()
	a.lingmaHTTP2 = enabled
	bridgeHandler := a.bridgeHandlerField
	a.mu.Unlock()

	if bridgeHandler != nil {
		bridgeHandler.SetLingmaHTTP2(enabled)
	}
	if a.db != nil {
		val := "false"
		if enabled {
			val = "true"
		}
		a.db.SaveSetting("lingma_http2", val)
	}
}

// GetStatus returns the current status.
func (a *App) GetStatus() map[string]interface{} {
	a.mu.Lock()
	status := map[string]interface{}{
		"proxy_running":    a.proxy != nil && a.proxy.Port() != 0,
		"gateway_running":  a.gatewayServer != nil,
		"gateway_logging":  a.gatewayLogging,
		"proxy_logging":    a.proxyLogging,
		"lingma_http2":     a.lingmaHTTP2,
		"authenticated":    a.bridgeHandlerField != nil,
		"auth_user":        a.authUser,
		"auth_expire_time": a.authExpireTime,
	}
	if a.db != nil {
		status["stats"] = a.db.Stats()
	}
	if a.hub != nil {
		status["ws_clients"] = a.hub.ClientCount()
	}
	oauthLogin := a.oauthLogin
	a.mu.Unlock()
	if oauthLogin != nil {
		oauthStatus := oauthLogin.Status()
		status["oauth_in_progress"] = oauthStatus.InProgress
		oauthExpiresAt := int64(0)
		if !oauthStatus.ExpiresAt.IsZero() {
			oauthExpiresAt = oauthStatus.ExpiresAt.UnixMilli()
		}
		status["oauth_expires_at"] = oauthExpiresAt
		status["oauth_error"] = oauthStatus.Error
	}
	return status
}

// GetModels returns the model list via Wails binding (avoids CORS issues).
func (a *App) GetModels() ([]bridge.ModelInfo, error) {
	a.mu.Lock()
	bridgeHandler := a.bridgeHandlerField
	a.mu.Unlock()
	if bridgeHandler == nil {
		return nil, fmt.Errorf("bridge not initialized")
	}
	return bridgeHandler.GetModels()
}

// OpenExternal opens a URL in the default browser.
func (a *App) OpenExternal(url string) {
	a.mu.Lock()
	openURL := a.openURL
	a.mu.Unlock()
	if openURL != nil {
		openURL(url)
	}
}

// StartOAuthLogin starts a browser-based Lingma OAuth flow for the desktop app.
func (a *App) StartOAuthLogin() error {
	a.mu.Lock()
	dataDir := a.dataDir
	oauthLogin := a.oauthLogin
	openURL := a.openURL
	a.mu.Unlock()
	if dataDir == "" {
		return fmt.Errorf("application data directory is not initialized")
	}
	if oauthLogin == nil {
		return fmt.Errorf("OAuth login is not initialized")
	}
	if openURL == nil {
		return fmt.Errorf("browser opener is not initialized")
	}

	machineID, err := auth.OAuthMachineID(dataDir)
	if err != nil {
		return fmt.Errorf("prepare OAuth machine ID: %w", err)
	}
	loginURL, err := oauthLogin.Start(machineID, func(creds *auth.Credentials) error {
		if err := auth.SaveExchangedCredentials(creds, dataDir); err != nil {
			return err
		}
		a.installCredentials(creds)
		return nil
	})
	if err != nil {
		return err
	}
	openURL(loginURL)
	return nil
}

// GetAnthropicMapping returns the current Anthropic model mapping.
func (a *App) GetAnthropicMapping() map[string]interface{} {
	if a.db == nil {
		return nil
	}
	mappingJSON, _ := a.db.GetSetting("anthropic_model_mapping")
	defaultModel, _ := a.db.GetSetting("default_anthropic_model")

	var mapping map[string]string
	if mappingJSON != "" {
		json.Unmarshal([]byte(mappingJSON), &mapping)
	}

	return map[string]interface{}{
		"mapping":       mapping,
		"default_model": defaultModel,
	}
}

// SaveAnthropicMapping saves the Anthropic model mapping.
func (a *App) SaveAnthropicMapping(mapping map[string]string, defaultModel string) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	effectiveMapping := mapping
	effectiveDefault := defaultModel
	if len(effectiveMapping) == 0 {
		effectiveMapping = bridge.DefaultAnthropicModelMapping()
		if effectiveDefault == "" {
			effectiveDefault = bridge.DefaultAnthropicModel
		}
	}
	mappingBytes, _ := json.Marshal(effectiveMapping)
	if err := a.db.SaveSetting("anthropic_model_mapping", string(mappingBytes)); err != nil {
		return err
	}
	if err := a.db.SaveSetting("default_anthropic_model", effectiveDefault); err != nil {
		return err
	}

	a.mu.Lock()
	bridgeHandler := a.bridgeHandlerField
	a.mu.Unlock()
	if bridgeHandler != nil {
		bridgeHandler.UpdateAnthropicMapping(effectiveMapping, effectiveDefault)
	}
	return nil
}

// GetVersion returns the current application version.
func (a *App) GetVersion() string {
	return resolveAppVersion()
}

func resolveAppVersion() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if version := gitDescribeVersion(); version != "" {
		return version
	}
	if version := vcsBuildVersion(); version != "" {
		return version
	}
	return Version
}

var (
	gitVersionOnce  sync.Once
	gitVersionCache string
)

func gitDescribeVersion() string {
	gitVersionOnce.Do(func() {
		gitVersionCache = gitDescribeVersionImpl()
	})
	return gitVersionCache
}

func gitDescribeVersionImpl() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rootCmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	rootOut, err := rootCmd.Output()
	if err != nil {
		return ""
	}
	repoRoot := strings.TrimSpace(string(rootOut))
	goMod, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil || !strings.Contains(string(goMod), "module github.com/coolxll/lingma-tap") {
		return ""
	}

	cmd := exec.CommandContext(ctx, "git", "describe", "--tags", "--always", "--dirty")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func vcsBuildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision == "" {
		return ""
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified == "true" {
		revision += "-dirty"
	}
	return revision
}

// Implement api.RecordStore interface
func (a *App) RecentRecords(limit int) ([]proto.Record, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	return a.db.RecentRecords(limit)
}

func (a *App) ClearTraffic() error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return a.db.ClearTraffic()
}

func (a *App) Stats() interface{} {
	if a.db == nil {
		return nil
	}
	return a.db.Stats()
}

// LogError logs an error message from the frontend to the backend logs.
func (a *App) LogError(message string) {
	if a.ctx != nil {
		runtime.LogError(a.ctx, message)
	}
	log.Printf("[frontend-error] %s", message)
}

func buildAppMenu(app *App) *menu.Menu {
	mainMenu := menu.NewMenu()
	mainMenu.Append(menu.AppMenu())
	mainMenu.Append(menu.EditMenu())

	windowMenu := mainMenu.AddSubmenu("Window")
	windowMenu.AddText("Close Window", keys.CmdOrCtrl("w"), func(_ *menu.CallbackData) {
		if app.ctx != nil {
			runtime.Hide(app.ctx)
		}
	})
	windowMenu.AddSeparator()
	windowMenu.Append(menu.WindowMenu())

	return mainMenu
}

func main() {
	assets, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		panic(err)
	}

	app := NewApp()
	if err := wails.Run(&options.App{
		Title:             "Lingma Tap",
		Width:             1400,
		Height:            900,
		MinWidth:          1000,
		MinHeight:         600,
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		Menu: buildAppMenu(app),
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
