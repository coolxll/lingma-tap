package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const lingmaBridgeStartTimeout = 20 * time.Second

// LingmaOfficialOAuthSession owns an isolated official Lingma process for one
// browser login. The official process generates and registers the nonce, serves
// the loopback callback, performs grantAuthInfos, and writes only to a temporary
// cache directory.
type LingmaOfficialOAuthSession struct {
	bridge    *lingmaOAuthBridge
	workDir   string
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

// StartLingmaOfficialOAuthSession starts the current Lingma authentication
// implementation in isolation and returns its one-time browser URL.
func StartLingmaOfficialOAuthSession(ctx context.Context, options OAuthOptions, binaryPath string) (*LingmaOfficialOAuthSession, OAuthStartInfo, error) {
	if ctx == nil {
		return nil, OAuthStartInfo{}, fmt.Errorf("OAuth context is required")
	}
	listenAddr := strings.TrimSpace(options.ListenAddr)
	if listenAddr == "" {
		listenAddr = defaultOAuthListenAddr
	}
	host, portText, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, OAuthStartInfo{}, fmt.Errorf("invalid OAuth callback address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, OAuthStartInfo{}, fmt.Errorf("OAuth callback address must be loopback")
	}
	httpPort, err := strconv.Atoi(portText)
	if err != nil || httpPort < 0 || httpPort > 65535 {
		return nil, OAuthStartInfo{}, fmt.Errorf("invalid OAuth callback port")
	}
	if httpPort == 0 {
		httpPort, err = reserveLoopbackPort()
		if err != nil {
			return nil, OAuthStartInfo{}, fmt.Errorf("reserve OAuth callback port: %w", err)
		}
		listenAddr = net.JoinHostPort(host, strconv.Itoa(httpPort))
	}

	timeout := options.Timeout
	if timeout <= 0 {
		timeout = oauthLoginTimeout
	}
	sessionCtx, cancel := context.WithTimeout(ctx, timeout)
	fail := func(err error) (*LingmaOfficialOAuthSession, OAuthStartInfo, error) {
		cancel()
		return nil, OAuthStartInfo{}, err
	}

	if strings.TrimSpace(binaryPath) == "" {
		binaryPath, err = FindLingmaServiceBinary()
		if err != nil {
			return fail(err)
		}
	}
	workDir, err := os.MkdirTemp("", "lingma-tap-oauth-")
	if err != nil {
		return fail(fmt.Errorf("create isolated Lingma work directory: %w", err))
	}
	cleanupWorkDir := true
	defer func() {
		if cleanupWorkDir {
			_ = os.RemoveAll(workDir)
		}
	}()

	socketPort, err := reserveLoopbackPort()
	if err != nil {
		return fail(fmt.Errorf("reserve Lingma socket port: %w", err))
	}
	for socketPort == httpPort {
		socketPort, err = reserveLoopbackPort()
		if err != nil {
			return fail(fmt.Errorf("reserve Lingma socket port: %w", err))
		}
	}
	bridge := &lingmaOAuthBridge{
		binaryPath: binaryPath,
		workDir:    workDir,
		socketPort: socketPort,
		httpPort:   httpPort,
	}
	if err := bridge.start(sessionCtx); err != nil {
		bridge.stop()
		return fail(err)
	}
	if err := bridge.initialize(sessionCtx); err != nil {
		bridge.stop()
		return fail(fmt.Errorf("initialize isolated Lingma service: %w", err))
	}

	loginURL, err := bridge.generateLoginURL(sessionCtx)
	if err != nil {
		bridge.stop()
		return fail(err)
	}
	expiresAt, _ := sessionCtx.Deadline()
	session := &LingmaOfficialOAuthSession{
		bridge:  bridge,
		workDir: workDir,
		ctx:     sessionCtx,
		cancel:  cancel,
	}
	cleanupWorkDir = false
	return session, OAuthStartInfo{
		LoginURL:        loginURL,
		CallbackAddress: listenAddr,
		ExpiresAt:       expiresAt,
	}, nil
}

// Wait waits for the official callback flow to produce a stable, complete
// credential cache. The returned values contain no raw callback URL.
func (s *LingmaOfficialOAuthSession) Wait() (OAuthResult, error) {
	if s == nil || s.bridge == nil {
		return OAuthResult{}, fmt.Errorf("OAuth session is nil")
	}
	credentials, err := s.bridge.waitForCredentialCache(s.ctx)
	if err != nil {
		if s.ctx.Err() == context.DeadlineExceeded {
			return OAuthResult{}, fmt.Errorf("OAuth login timed out")
		}
		if s.ctx.Err() != nil {
			return OAuthResult{}, fmt.Errorf("OAuth login cancelled")
		}
		return OAuthResult{}, err
	}
	return OAuthResult{
		Callback: OAuthCallback{
			UID:                credentials.UID,
			AID:                credentials.AID,
			Name:               credentials.Name,
			SecurityOAuthToken: credentials.SecurityOAuthToken,
			RefreshToken:       credentials.RefreshToken,
			ExpireTime:         credentials.ExpireTime,
		},
		Credentials: credentials,
	}, nil
}

// Close stops the isolated Lingma process and removes its temporary cache.
func (s *LingmaOfficialOAuthSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.cancel()
		if s.bridge != nil {
			s.bridge.stop()
		}
		if s.workDir != "" {
			_ = os.RemoveAll(s.workDir)
		}
	})
	return nil
}

type lingmaOAuthBridge struct {
	binaryPath string
	workDir    string
	socketPort int
	httpPort   int

	cmd      *exec.Cmd
	done     chan struct{}
	waitOnce sync.Once
	conn     *websocket.Conn
}

func (b *lingmaOAuthBridge) start(ctx context.Context) error {
	b.cmd = exec.Command(
		b.binaryPath,
		"start",
		"--transportType=websocket",
		fmt.Sprintf("--workDir=%s", b.workDir),
		fmt.Sprintf("--socketPort=%d", b.socketPort),
		fmt.Sprintf("--httpPort=%d", b.httpPort),
	)
	configureLingmaCommand(b.cmd)
	b.cmd.Stdout = io.Discard
	b.cmd.Stderr = io.Discard
	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("start isolated Lingma service: %w", err)
	}
	b.done = make(chan struct{})
	go func() {
		_ = b.cmd.Wait()
		close(b.done)
	}()

	deadline := time.Now().Add(lingmaBridgeStartTimeout)
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d", b.socketPort)
	for time.Now().Before(deadline) {
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
		if err == nil {
			b.conn = conn
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.done:
			return fmt.Errorf("isolated Lingma service exited during startup")
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("isolated Lingma service did not start within %s", lingmaBridgeStartTimeout)
}

func (b *lingmaOAuthBridge) initialize(ctx context.Context) error {
	if b.conn == nil {
		return fmt.Errorf("Lingma service connection is not ready")
	}
	if err := b.writeRPC(1, "initialize", map[string]any{
		"processId":    nil,
		"clientInfo":   map[string]string{"name": "lingma-tap-oauth-probe", "version": "1.0"},
		"rootUri":      "file:///tmp/lingma-tap-oauth-probe",
		"capabilities": map[string]any{},
		"workspaceFolders": []map[string]string{
			{"uri": "file:///tmp/lingma-tap-oauth-probe", "name": "lingma-tap-oauth-probe"},
		},
	}); err != nil {
		return err
	}
	if err := b.readRPCResponse(ctx, 1, nil); err != nil {
		return err
	}
	return b.writeRPCNotification("initialized", map[string]any{})
}

func (b *lingmaOAuthBridge) generateLoginURL(ctx context.Context) (string, error) {
	if err := b.writeRPC(2, "login/generateUrl", map[string]string{
		"loginDedicatedType": "",
	}); err != nil {
		return "", fmt.Errorf("request official Lingma login URL: %w", err)
	}
	var result struct {
		LoginURL  string `json:"loginUrl"`
		Success   *bool  `json:"success"`
		ErrorCode string `json:"errorCode"`
		ErrorMsg  string `json:"errorMsg"`
	}
	if err := b.readRPCResponse(ctx, 2, &result); err != nil {
		return "", fmt.Errorf("request official Lingma login URL: %w", err)
	}
	if result.Success != nil && !*result.Success {
		if result.ErrorCode != "" {
			return "", fmt.Errorf("Lingma could not generate a login URL (code %s)", result.ErrorCode)
		}
		return "", fmt.Errorf("Lingma could not generate a login URL")
	}
	loginURL := strings.TrimSpace(result.LoginURL)
	parsed, err := url.Parse(loginURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "devops.aliyun.com" {
		return "", fmt.Errorf("Lingma returned an invalid login URL")
	}
	if parsed.Query().Get("port") != strconv.Itoa(b.httpPort) {
		return "", fmt.Errorf("Lingma returned a login URL for the wrong callback port")
	}
	return loginURL, nil
}

func (b *lingmaOAuthBridge) waitForCredentialCache(ctx context.Context) (*Credentials, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	cacheDir := filepath.Join(b.workDir, "cache")
	var cacheSignature string
	var cacheStableSince time.Time
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-b.done:
			return nil, fmt.Errorf("isolated Lingma service exited before OAuth completed")
		case <-ticker.C:
			credentials, err := LoadCredentialsFromDir(cacheDir)
			if err != nil || credentials.CosyKey == "" || credentials.EncryptUserInfo == "" || credentials.UID == "" {
				cacheSignature = ""
				cacheStableSince = time.Time{}
				continue
			}
			signature, err := lingmaCacheSignature(cacheDir)
			if err != nil {
				continue
			}
			if signature != cacheSignature {
				cacheSignature = signature
				cacheStableSince = time.Now()
				continue
			}
			if !cacheStableSince.IsZero() && time.Since(cacheStableSince) >= 3*time.Second {
				return credentials, nil
			}
		}
	}
}

func lingmaCacheSignature(cacheDir string) (string, error) {
	var parts []string
	for _, name := range []string{"id", "user"} {
		info, err := os.Stat(filepath.Join(cacheDir, name))
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", name, info.Size(), info.ModTime().UnixNano()))
	}
	return strings.Join(parts, ";"), nil
}

func (b *lingmaOAuthBridge) writeRPC(id int, method string, params any) error {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload)
	return b.conn.WriteMessage(websocket.TextMessage, []byte(frame))
}

func (b *lingmaOAuthBridge) writeRPCNotification(method string, params any) error {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload)
	return b.conn.WriteMessage(websocket.TextMessage, []byte(frame))
}

func (b *lingmaOAuthBridge) readRPCResponse(ctx context.Context, expectedID int, result any) error {
	readDeadline := time.Now().Add(lingmaBridgeStartTimeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(readDeadline) {
		readDeadline = deadline
	}
	if err := b.conn.SetReadDeadline(readDeadline); err != nil {
		return err
	}
	defer b.conn.SetReadDeadline(time.Time{})

	for {
		_, raw, err := b.conn.ReadMessage()
		if err != nil {
			return err
		}
		payload := raw
		if separator := strings.Index(string(raw), "\r\n\r\n"); separator >= 0 {
			payload = raw[separator+4:]
		}
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(payload, &response) != nil || response.ID != expectedID {
			continue
		}
		if response.Error != nil {
			message := strings.TrimSpace(strings.ReplaceAll(response.Error.Message, "\n", " "))
			if len(message) > 160 {
				message = message[:160]
			}
			if message != "" {
				return fmt.Errorf("RPC %d: %s", response.Error.Code, message)
			}
			return fmt.Errorf("RPC %d", response.Error.Code)
		}
		if result != nil && len(response.Result) != 0 && string(response.Result) != "null" {
			if err := json.Unmarshal(response.Result, result); err != nil {
				return fmt.Errorf("decode Lingma RPC result: %w", err)
			}
		}
		return nil
	}
}

func (b *lingmaOAuthBridge) stop() {
	b.waitOnce.Do(func() {
		if b.conn != nil {
			_ = b.conn.Close()
		}
		if b.cmd == nil || b.cmd.Process == nil || b.done == nil {
			return
		}
		select {
		case <-b.done:
			return
		default:
		}
		requestLingmaProcessStop(b.cmd)
		select {
		case <-b.done:
		case <-time.After(3 * time.Second):
			_ = b.cmd.Process.Kill()
			<-b.done
		}
	})
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
