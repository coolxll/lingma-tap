package auth

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	callbackAuthFixture  = "dVukDLuHgMDp$$SptjRa(ozgz#,JSLzG@TYVf)iP"
	callbackTokenFixture = "V@NIV^#fj@NQV@_QV_$$NEptJHWUDSItJOgrJSacNZptJHWUDSItJOgrJSac"
)

func TestParseOAuthCallback(t *testing.T) {
	callback, err := ParseOAuthCallback(callbackAuthFixture, callbackTokenFixture)
	if err != nil {
		t.Fatalf("ParseOAuthCallback error: %v", err)
	}
	if callback.UID != "uid-123" || callback.AID != "aid-456" || callback.Name != "Ada Lovelace" {
		t.Fatalf("unexpected auth callback: %+v", callback)
	}
	if callback.SecurityOAuthToken != "pt-token-value" || callback.RefreshToken != "rt-token-value" || callback.ExpireTime != 1783605791090 {
		t.Fatalf("unexpected token callback: %+v", callback)
	}
}

func TestParseOAuthCallbackRejectsInvalidValues(t *testing.T) {
	if _, err := ParseOAuthCallback("?", callbackTokenFixture); err == nil {
		t.Fatal("expected invalid custom alphabet error")
	}
	if _, err := ParseOAuthCallback(callbackAuthFixture, callbackAuthFixture); err == nil {
		t.Fatal("expected invalid token format error")
	}
}

func TestCredentialsFromOAuthGeneratesCosyCredentials(t *testing.T) {
	creds, err := CredentialsFromOAuth(OAuthCallback{
		UID:                "uid-123",
		AID:                "aid-456",
		Name:               "Ada Lovelace",
		SecurityOAuthToken: "pt-token-value",
		RefreshToken:       "rt-token-value",
		ExpireTime:         1783605791090,
	}, "12345678-1234-1234-1234-123456789012")
	if err != nil {
		t.Fatalf("CredentialsFromOAuth error: %v", err)
	}
	if creds.UserType != "personal_standard" || creds.CosyKey == "" || creds.EncryptUserInfo == "" {
		t.Fatalf("incomplete generated credentials: %+v", creds)
	}
	key, err := base64.StdEncoding.DecodeString(creds.CosyKey)
	if err != nil {
		t.Fatalf("decode COSY key: %v", err)
	}
	if len(key) != serverPubKey.Size() {
		t.Fatalf("RSA key length = %d, want %d", len(key), serverPubKey.Size())
	}
	info, err := base64.StdEncoding.DecodeString(creds.EncryptUserInfo)
	if err != nil {
		t.Fatalf("decode encrypted user info: %v", err)
	}
	if len(info) == 0 || len(info)%16 != 0 {
		t.Fatalf("encrypted user info has invalid AES block length: %d", len(info))
	}
	if session := NewSession(creds); session.CosyKey != creds.CosyKey {
		t.Fatalf("session key = %q, want persisted COSY key", session.CosyKey)
	}
}

func TestSaveExchangedCredentialsRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	creds := &Credentials{
		MachineID:          "12345678-1234-1234-1234-123456789012",
		UID:                "uid-123",
		AID:                "aid-456",
		Name:               "Ada Lovelace",
		OrganizationID:     "org-1",
		UserType:           "personal_standard",
		CosyKey:            "generated-cosy-key",
		EncryptUserInfo:    "generated-encrypted-info",
		SecurityOAuthToken: "pt-token-value",
		RefreshToken:       "rt-token-value",
		ExpireTime:         1783605791090,
	}
	if err := SaveExchangedCredentials(creds, dataDir); err != nil {
		t.Fatalf("SaveExchangedCredentials error: %v", err)
	}

	id, err := readTrimmed(filepath.Join(dataDir, "auth", "id"))
	if err != nil {
		t.Fatalf("read persisted machine ID: %v", err)
	}
	if id != creds.MachineID {
		t.Fatalf("persisted machine ID = %q, want raw ID %q", id, creds.MachineID)
	}
	loaded, err := LoadCredentialsFromDir(filepath.Join(dataDir, "auth"))
	if err != nil {
		t.Fatalf("LoadCredentialsFromDir error: %v", err)
	}
	if loaded.AID != creds.AID || loaded.CosyKey != creds.CosyKey || loaded.RefreshToken != creds.RefreshToken {
		t.Fatalf("credentials did not round trip: %+v", loaded)
	}
}

func TestOAuthLoginCallbackCompletesOnce(t *testing.T) {
	login := NewOAuthLogin()
	defer login.Close()

	completed := make(chan *Credentials, 1)
	loginURL, err := login.Start("12345678-1234-1234-1234-123456789012", func(creds *Credentials) error {
		completed <- creds
		return nil
	})
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	outer, err := url.Parse(loginURL)
	if err != nil {
		t.Fatalf("parse login URL: %v", err)
	}
	if outer.Host != "devops.aliyun.com" || outer.Path != "/lingma/login" || outer.Query().Get("state") == "" || outer.Query().Get("challenge_method") != "S256" {
		t.Fatalf("unexpected login URL: %s", outer.String())
	}

	login.mu.Lock()
	callbackAddr := login.listener.Addr().String()
	login.mu.Unlock()
	query := url.Values{
		"state": {outer.Query().Get("state")},
		"auth":  {callbackAuthFixture},
		"token": {callbackTokenFixture},
	}
	resp, err := http.Get("http://" + callbackAddr + "/auth/callback?" + query.Encode())
	if err != nil {
		t.Fatalf("send OAuth callback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", resp.StatusCode)
	}

	select {
	case creds := <-completed:
		if creds.UID != "uid-123" || creds.MachineID == "" {
			t.Fatalf("unexpected callback credentials: %+v", creds)
		}
	case <-time.After(time.Second):
		t.Fatal("OAuth completion was not called")
	}
	if status := login.Status(); status.InProgress || status.Error != "" {
		t.Fatalf("unexpected login status after callback: %+v", status)
	}
}

func TestOAuthLoginRejectsInvalidStateWithoutConsumingFlow(t *testing.T) {
	login := NewOAuthLogin()
	defer login.Close()

	if _, err := login.Start("12345678-1234-1234-1234-123456789012", func(*Credentials) error { return nil }); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	login.mu.Lock()
	callbackAddr := login.listener.Addr().String()
	login.mu.Unlock()
	resp, err := http.Get("http://" + callbackAddr + "/auth/callback?state=wrong")
	if err != nil {
		t.Fatalf("send invalid callback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid callback status = %d, want 403", resp.StatusCode)
	}
	if !login.Status().InProgress {
		t.Fatal("invalid state consumed active OAuth login")
	}
}

func TestOAuthLoginTimesOut(t *testing.T) {
	login := NewOAuthLogin()
	login.timeout = 20 * time.Millisecond
	defer login.Close()

	if _, err := login.Start("12345678-1234-1234-1234-123456789012", func(*Credentials) error { return nil }); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for login.Status().InProgress && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	status := login.Status()
	if status.InProgress || !strings.Contains(status.Error, "timed out") {
		t.Fatalf("unexpected timeout status: %+v", status)
	}
}
