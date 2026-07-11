package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	oauthLoginTimeout     = 5 * time.Minute
	oauthCustomAlphabet   = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
	oauthStandardAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)

// OAuthCallback is the decoded V2 callback payload returned by Lingma's
// browser login flow. It intentionally contains no raw URL or callback state.
type OAuthCallback struct {
	UID                string
	AID                string
	Name               string
	SecurityOAuthToken string
	RefreshToken       string
	ExpireTime         int64
}

// OAuthLoginStatus contains only display-safe state for the desktop UI.
type OAuthLoginStatus struct {
	InProgress bool
	ExpiresAt  time.Time
	Error      string
}

// OAuthLogin owns the short-lived loopback callback server for one desktop
// browser login at a time.
type OAuthLogin struct {
	mu         sync.Mutex
	listener   net.Listener
	server     *http.Server
	state      string
	machineID  string
	expiresAt  time.Time
	inProgress bool
	lastError  string
	onComplete func(*Credentials) error

	timeout time.Duration
	now     func() time.Time
	listen  func(network, address string) (net.Listener, error)
	random  io.Reader
}

func NewOAuthLogin() *OAuthLogin {
	return &OAuthLogin{
		timeout: oauthLoginTimeout,
		now:     time.Now,
		listen:  net.Listen,
		random:  rand.Reader,
	}
}

// Start opens a loopback callback listener and returns the China-region login
// URL. The caller is responsible for opening the URL in the default browser.
func (l *OAuthLogin) Start(machineID string, onComplete func(*Credentials) error) (string, error) {
	if strings.TrimSpace(machineID) == "" {
		return "", fmt.Errorf("machine ID is required")
	}
	if onComplete == nil {
		return "", fmt.Errorf("OAuth completion callback is required")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inProgress {
		return "", fmt.Errorf("OAuth login is already in progress")
	}

	listener, err := l.listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("start OAuth callback listener: %w", err)
	}

	nonce, err := randomHex(l.random, 16)
	if err != nil {
		_ = listener.Close()
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	verifier, challenge, err := newPKCE(l.random)
	if err != nil {
		_ = listener.Close()
		return "", fmt.Errorf("generate PKCE challenge: %w", err)
	}
	_ = verifier // The Lingma browser endpoint consumes the challenge server-side.

	state := "2-" + nonce
	port := listener.Addr().(*net.TCPAddr).Port
	loginURL, err := buildChinaOAuthURL(state, nonce, challenge, machineID, port)
	if err != nil {
		_ = listener.Close()
		return "", err
	}

	l.state = state
	l.machineID = machineID
	l.expiresAt = l.now().Add(l.timeout)
	l.inProgress = true
	l.lastError = ""
	l.listener = listener
	l.onComplete = onComplete

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", l.handleCallback)
	l.server = &http.Server{Handler: mux}
	server := l.server

	go func() {
		_ = server.Serve(listener)
	}()
	go l.expire(state, server)

	return loginURL, nil
}

func (l *OAuthLogin) expire(state string, server *http.Server) {
	timer := time.NewTimer(l.timeout)
	defer timer.Stop()
	<-timer.C

	l.mu.Lock()
	if !l.inProgress || l.state != state {
		l.mu.Unlock()
		return
	}
	l.clearLocked("OAuth login timed out")
	l.mu.Unlock()
	_ = server.Close()
}

func (l *OAuthLogin) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state := r.URL.Query().Get("state")
	l.mu.Lock()
	if !l.inProgress || state == "" || state != l.state {
		l.mu.Unlock()
		http.Error(w, "Invalid or expired OAuth state", http.StatusForbidden)
		return
	}
	onComplete := l.onComplete
	server := l.server
	machineID := l.machineID
	l.clearLocked("") // Consume a matching state before parsing callback data.
	l.mu.Unlock()
	if server != nil {
		go func() { _ = server.Shutdown(context.Background()) }()
	}

	callback, err := ParseOAuthCallback(r.URL.Query().Get("auth"), r.URL.Query().Get("token"))
	if err != nil {
		l.setError("OAuth callback could not be processed")
		writeOAuthResult(w, http.StatusBadRequest, "Authentication failed", "The login callback was invalid. Return to Lingma Tap and try again.")
		return
	}

	// The machine ID is attached to the closure when the callback handler is
	// started. The callback URL itself never carries credential material.
	creds, err := CredentialsFromOAuth(callback, machineID)
	if err != nil {
		l.setError("OAuth credentials could not be created")
		writeOAuthResult(w, http.StatusInternalServerError, "Authentication failed", "Lingma Tap could not prepare local credentials. Try again.")
		return
	}
	if err := onComplete(creds); err != nil {
		l.setError("OAuth credentials could not be saved")
		writeOAuthResult(w, http.StatusInternalServerError, "Authentication failed", "Lingma Tap could not save the login. Try again.")
		return
	}

	l.setError("")
	writeOAuthResult(w, http.StatusOK, "Authentication successful", "You can close this window and return to Lingma Tap.")
}

func (l *OAuthLogin) clearLocked(lastError string) {
	l.listener = nil
	l.server = nil
	l.state = ""
	l.machineID = ""
	l.expiresAt = time.Time{}
	l.inProgress = false
	l.onComplete = nil
	l.lastError = lastError
}

func (l *OAuthLogin) setError(message string) {
	l.mu.Lock()
	l.lastError = message
	l.mu.Unlock()
}

// Status returns display-safe progress and error state.
func (l *OAuthLogin) Status() OAuthLoginStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return OAuthLoginStatus{
		InProgress: l.inProgress,
		ExpiresAt:  l.expiresAt,
		Error:      l.lastError,
	}
}

// Close stops a pending callback listener during application shutdown.
func (l *OAuthLogin) Close() {
	l.mu.Lock()
	server := l.server
	l.clearLocked("")
	l.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
}

// ParseOAuthCallback decodes the Lingma V2 callback values. auth and token
// each contain exactly three newline-separated values after custom decoding.
func ParseOAuthCallback(authParam, tokenParam string) (OAuthCallback, error) {
	authRaw, err := decodeOAuthValue(authParam)
	if err != nil {
		return OAuthCallback{}, fmt.Errorf("decode auth: %w", err)
	}
	tokenRaw, err := decodeOAuthValue(tokenParam)
	if err != nil {
		return OAuthCallback{}, fmt.Errorf("decode token: %w", err)
	}

	authParts, err := splitOAuthParts(string(authRaw))
	if err != nil {
		return OAuthCallback{}, fmt.Errorf("parse auth: %w", err)
	}
	tokenParts, err := splitOAuthParts(string(tokenRaw))
	if err != nil {
		return OAuthCallback{}, fmt.Errorf("parse token: %w", err)
	}
	expireTime, err := strconv.ParseInt(tokenParts[2], 10, 64)
	if err != nil || expireTime <= 0 {
		return OAuthCallback{}, fmt.Errorf("parse token expiry")
	}
	if !strings.HasPrefix(tokenParts[0], "pt-") || !strings.HasPrefix(tokenParts[1], "rt-") {
		return OAuthCallback{}, fmt.Errorf("unexpected OAuth token format")
	}

	return OAuthCallback{
		UID:                authParts[0],
		AID:                authParts[1],
		Name:               authParts[2],
		SecurityOAuthToken: tokenParts[0],
		RefreshToken:       tokenParts[1],
		ExpireTime:         expireTime,
	}, nil
}

func splitOAuthParts(value string) ([]string, error) {
	parts := strings.Split(value, "\n")
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected three fields")
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return nil, fmt.Errorf("field %d is empty", i+1)
		}
	}
	return parts, nil
}

func decodeOAuthValue(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("value is empty")
	}
	if dollar := strings.IndexByte(value, '$'); dollar >= 0 {
		end := dollar
		for end < len(value) && value[end] == '$' {
			end++
		}
		value = value[:dollar] + value[end:]
	}
	if value == "" {
		return nil, fmt.Errorf("value is empty after padding removal")
	}

	blockSize := (len(value) + 2) / 3
	lastBlockSize := len(value) - 2*blockSize
	if lastBlockSize < 0 || lastBlockSize+blockSize > len(value) {
		return nil, fmt.Errorf("invalid block layout")
	}
	b2 := value[:lastBlockSize]
	b1 := value[lastBlockSize : lastBlockSize+blockSize]
	b0 := value[lastBlockSize+blockSize:]

	std := make([]byte, len(value))
	for i, c := range []byte(b0 + b1 + b2) {
		index := strings.IndexByte(oauthCustomAlphabet, c)
		if index < 0 {
			return nil, fmt.Errorf("invalid OAuth encoding character")
		}
		std[i] = oauthStandardAlphabet[index]
	}
	return base64.RawStdEncoding.DecodeString(string(std))
}

// CredentialsFromOAuth creates the locally generated COSY credentials that
// the gateway uses for Bearer signing after browser login completes.
func CredentialsFromOAuth(callback OAuthCallback, machineID string) (*Credentials, error) {
	if len(machineID) < aes.BlockSize {
		return nil, fmt.Errorf("machine ID is too short")
	}
	creds := &Credentials{
		MachineID:          machineID,
		UID:                callback.UID,
		AID:                callback.AID,
		Name:               callback.Name,
		UserType:           "personal_standard",
		SecurityOAuthToken: callback.SecurityOAuthToken,
		RefreshToken:       callback.RefreshToken,
		ExpireTime:         callback.ExpireTime,
	}
	if err := generateCosyCredentials(creds, rand.Reader); err != nil {
		return nil, err
	}
	return creds, nil
}

func generateCosyCredentials(creds *Credentials, random io.Reader) error {
	keyBytes := make([]byte, 8)
	if _, err := io.ReadFull(random, keyBytes); err != nil {
		return fmt.Errorf("generate COSY key: %w", err)
	}
	aesKey := []byte(hex.EncodeToString(keyBytes))

	encryptedKey, err := rsa.EncryptPKCS1v15(random, serverPubKey, aesKey)
	if err != nil {
		return fmt.Errorf("encrypt COSY key: %w", err)
	}
	inner := map[string]string{
		"name":                 creds.Name,
		"aid":                  creds.AID,
		"uid":                  creds.UID,
		"yx_uid":               "",
		"organization_id":      creds.OrganizationID,
		"organization_name":    "",
		"user_type":            creds.UserType,
		"security_oauth_token": creds.SecurityOAuthToken,
		"refresh_token":        creds.RefreshToken,
	}
	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return fmt.Errorf("encode COSY user info: %w", err)
	}
	encryptedInfo, err := encryptWithAESKey(innerJSON, aesKey)
	if err != nil {
		return fmt.Errorf("encrypt COSY user info: %w", err)
	}

	creds.CosyKey = base64.StdEncoding.EncodeToString(encryptedKey)
	creds.EncryptUserInfo = base64.StdEncoding.EncodeToString(encryptedInfo)
	return nil
}

func encryptWithAESKey(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padding)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key).CryptBlocks(ciphertext, padded)
	return ciphertext, nil
}

func randomHex(random io.Reader, bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := io.ReadFull(random, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func newPKCE(random io.Reader) (string, string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(random, buf); err != nil {
		return "", "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(buf)
	digest := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func buildChinaOAuthURL(state, nonce, challenge, machineID string, port int) (string, error) {
	if port <= 0 || port > 65535 {
		return "", fmt.Errorf("invalid OAuth callback port")
	}
	inner := &url.URL{Scheme: "https", Host: "devops.aliyun.com", Path: "/lingma/login"}
	query := inner.Query()
	query.Set("nonce", nonce)
	query.Set("port", strconv.Itoa(port))
	query.Set("state", state)
	query.Set("challenge", challenge)
	query.Set("challenge_method", "S256")
	query.Set("machine_id", machineID)
	inner.RawQuery = query.Encode()

	outer := &url.URL{Scheme: "https", Host: "account.alibabacloud.com", Path: "/login/login.htm"}
	outerQuery := outer.Query()
	outerQuery.Set("oauth_callback", inner.String())
	outer.RawQuery = outerQuery.Encode()
	return outer.String(), nil
}

func writeOAuthResult(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><html><head><meta charset=\"utf-8\"><title>%s</title></head><body><main><h1>%s</h1><p>%s</p></main></body></html>", title, title, message)
}
