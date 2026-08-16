package auth

import (
	"encoding/base64"
	"path/filepath"
	"testing"
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
