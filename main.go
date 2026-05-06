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
	"path/filepath"
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
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:web/dist
var webAssets embed.FS

type App struct {
	ctx                context.Context
	mu                 sync.Mutex
	ca                 *ca.CA
	db                 *storage.DB
	sink               *storage.AsyncSink
	hub                *api.Hub
	proxy              *proxy.Server
	bridgeHandlerField *bridge.BridgeHandler
	apiLn              net.Listener
	proxyRunning       bool
	proxyPort          int
	gatewayRunning     bool
	gatewayServer      *http.Server
	gatewayLogging     bool
}

func NewApp() *App {
	return &App{}
}

func convertGatewayLogToRecord(l *proto.GatewayLog) *proto.Record {
	return &proto.Record{
		ID:           l.ID,
		Ts:           l.Ts,
		Session:      l.Session,
		Source:       "gateway",
		Method:       l.Method,
		Path:         l.Path,
		EndpointType: "chat",
		ReqBody:      l.RequestBody,
		RespBody:     l.ResponseBody,
		Status:       l.Status,
		IsSSE:        l.IsSSE,
		SSEEvents:    l.SSEEvents,
		Error:        l.Error,
		Model:        l.Model,
		InputTokens:  l.InputTokens,
		OutputTokens: l.OutputTokens,
		Latency:      l.Latency,
		ReqHeaders:   make(map[string]string),
		RespHeaders:  make(map[string]string),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".lingma-tap")
	os.MkdirAll(dataDir, 0755)

	// Persist logs to file
	logFile, err := os.OpenFile(filepath.Join(dataDir, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
		log.Println("--- App Started ---")
	}

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

	// Initialize WebSocket Hub
	a.hub = api.NewHub()
	go a.hub.Run()

	// Start API server (WebSocket + REST + Bridge)
	var bridgeHandler api.BridgeHandler
	creds, err := auth.LoadCredentials()
	if err != nil {
		log.Printf("[app] LocalAuth not available (bridge disabled): %v", err)
	} else {
		session := auth.NewSession(creds)
		a.bridgeHandlerField = bridge.NewBridgeHandler(session, func(log *proto.GatewayLog) {
			a.mu.Lock()
			logging := a.gatewayLogging
			a.mu.Unlock()

			if logging && a.sink != nil {
				a.db.SaveGatewayLog(log) // Use direct DB call for now or update sink
			}
			if a.hub != nil {
				// We need to broadcast as a general record for the UI to show it?
				// Actually, the UI GatewayMonitor expects TrafficRecord.
				// I'll convert it or update the UI.
				// For now, I'll convert it to a TrafficRecord for the real-time feed.
				rec := convertGatewayLogToRecord(log)
				a.hub.Broadcast(rec)
			}
		})
		if os.Getenv("GATEWAY_DEBUG") == "1" {
			a.bridgeHandlerField.SetDebug(true)
		}
		bridgeHandler = a.bridgeHandlerField
		log.Printf("[app] Bridge initialized for user %s", creds.Name)

		// Load Anthropic model mapping from settings
		mappingJSON, _ := a.db.GetSetting("anthropic_model_mapping")
		defaultModel, _ := a.db.GetSetting("default_anthropic_model")
		if mappingJSON != "" {
			var mapping map[string]string
			if err := json.Unmarshal([]byte(mappingJSON), &mapping); err == nil {
				a.bridgeHandlerField.UpdateAnthropicMapping(mapping, defaultModel)
			}
		} else {
			// Fallback to hardcoded defaults if DB migration didn't run or something
			defaults := map[string]string{
				"sonnet": "dashscope_qwen3_coder",
				"haiku":  "dashscope_qmodel",
				"opus":   "dashscope_qwen_max_latest",
			}
			a.bridgeHandlerField.UpdateAnthropicMapping(defaults, "dashscope_qmodel")
		}
	}

	handler := api.NewHandler(a.hub, a, bridgeHandler)
	mux := http.NewServeMux()
	handler.RegisterInternalRoutes(mux)

	a.apiLn, err = net.Listen("tcp", "127.0.0.1:9091")
	if err != nil {
		log.Printf("[app] API listen error: %v", err)
		return
	}
	go http.Serve(a.apiLn, mux)
	log.Printf("[app] Management API server on %s", a.apiLn.Addr())
	a.gatewayLogging = true

	a.proxy = proxy.NewServer(a.ca, func(rec *proto.Record) {
		rec.Source = "proxy"
		a.mu.Lock()
		logging := a.gatewayLogging
		a.mu.Unlock()

		if logging && a.sink != nil {
			a.sink.SaveRecord(rec)
		}
		if a.hub != nil {
			a.hub.Broadcast(rec)
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
		if err := a.StartGateway(9090); err != nil {
			log.Printf("[app] Auto-start Gateway error: %v", err)
		} else {
			log.Printf("[app] Auto-started AI Gateway on port 9090")
		}
	}()
}

func (a *App) shutdown(ctx context.Context) {
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

// StartGateway starts the AI Gateway.
func (a *App) StartGateway(port int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.gatewayServer != nil {
		a.gatewayServer.Close()
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	handler := api.NewHandler(a.hub, a, a.bridgeHandlerField)
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

// ClearRecords clears all traffic data.
func (a *App) ClearRecords() error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return a.db.ClearTraffic()
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

// SetLogging enables or disables traffic logging to SQLite.
func (a *App) SetLogging(enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gatewayLogging = enabled
}

// GetStatus returns the current status.
func (a *App) GetStatus() map[string]interface{} {
	a.mu.Lock()
	defer a.mu.Unlock()

	status := map[string]interface{}{
		"proxy_running":   a.proxy != nil,
		"gateway_running": a.gatewayServer != nil,
		"gateway_logging": a.gatewayLogging,
	}
	if a.db != nil {
		status["stats"] = a.db.Stats()
	}
	if a.hub != nil {
		status["ws_clients"] = a.hub.ClientCount()
	}
	return status
}

// GetModels returns the model list via Wails binding (avoids CORS issues).
func (a *App) GetModels() ([]bridge.ModelInfo, error) {
	if a.bridgeHandlerField == nil {
		return nil, fmt.Errorf("bridge not initialized")
	}
	return a.bridgeHandlerField.GetModels()
}

// OpenExternal opens a URL in the default browser.
func (a *App) OpenExternal(url string) {
	runtime.BrowserOpenURL(a.ctx, url)
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
	mappingBytes, _ := json.Marshal(mapping)
	if err := a.db.SaveSetting("anthropic_model_mapping", string(mappingBytes)); err != nil {
		return err
	}
	if err := a.db.SaveSetting("default_anthropic_model", defaultModel); err != nil {
		return err
	}

	if a.bridgeHandlerField != nil {
		a.bridgeHandlerField.UpdateAnthropicMapping(mapping, defaultModel)
	}
	return nil
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

func main() {
	assets, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		panic(err)
	}

	app := NewApp()
	if err := wails.Run(&options.App{
		Title:     "Lingma Tap",
		Width:     1400,
		Height:    900,
		MinWidth:  1000,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
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
