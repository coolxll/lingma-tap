package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

func init() {
	_ = godotenv.Load("../../.env")
}

func TestLoadCredentials(t *testing.T) {
	creds, err := LoadCredentials()
	if err != nil {
		t.Skipf("auth files not found: %v", err)
	}

	if creds.MachineID == "" {
		t.Error("empty MachineID")
	}
	if creds.UID == "" {
		t.Error("empty UID")
	}
	if creds.CosyKey == "" {
		t.Error("empty CosyKey")
	}
}

func TestSessionSignRequest(t *testing.T) {
	creds, err := LoadCredentials()
	if err != nil {
		t.Skipf("auth files not found: %v", err)
	}

	sess := NewSession(creds)
	cosyDate := time.Now().Format("20060102150405")
	bearer, err := sess.BuildBearer("test-body", "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?Encode=1", cosyDate)
	if err != nil {
		t.Fatal(err)
	}

	if bearer == "" {
		t.Error("empty bearer")
	}
}

func TestEncryptDecryptUser(t *testing.T) {
	machineID := os.Getenv("LINGMA_MID")
	if machineID == "" {
		t.Skip("Skipping encryption test: LINGMA_MID not set")
	}
	plaintext := []byte(`{"name":"Test User","uid":"12345"}`)

	encrypted, err := encryptUser(plaintext, machineID)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	decrypted, err := decryptUser(encrypted, machineID)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted mismatch: got %s, want %s", string(decrypted), string(plaintext))
	}
}

func TestLoadQoderCNDir(t *testing.T) {
	tempDir := t.TempDir()
	qoderDir := filepath.Join(tempDir, ".qoder-cn")
	authDir := filepath.Join(qoderDir, ".auth")
	if err := os.MkdirAll(authDir, 0755); err != nil {
		t.Fatal(err)
	}

	machineID := "1234567890abcdef12345678"
	if err := os.WriteFile(filepath.Join(authDir, "id"), []byte(machineID), 0644); err != nil {
		t.Fatal(err)
	}

	userJSON := []byte(`{
		"name": "Qoder User",
		"uid": "qoder-12345",
		"key": "cosy-key-abc",
		"encrypt_user_info": "enc-info",
		"user_type": "vip",
		"security_oauth_token": "sec-token",
		"access_token": "acc-token",
		"refresh_token": "ref-token",
		"expire_time": 1780000000000
	}`)
	encryptedUser, err := encryptUser(userJSON, machineID)
	if err != nil {
		t.Fatalf("encryptUser: %v", err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "user"), []byte(encryptedUser), 0644); err != nil {
		t.Fatal(err)
	}

	creds, err := loadFromDir(qoderDir)
	if err != nil {
		t.Fatalf("loadFromDir failed on qoderDir: %v", err)
	}

	if creds.UID != "qoder-12345" {
		t.Errorf("expected UID 'qoder-12345', got %q", creds.UID)
	}
	if creds.CosyKey != "cosy-key-abc" {
		t.Errorf("expected CosyKey 'cosy-key-abc', got %q", creds.CosyKey)
	}
	if creds.AccessToken != "acc-token" {
		t.Errorf("expected AccessToken 'acc-token', got %q", creds.AccessToken)
	}
	if creds.ExpireTime != 1780000000000 {
		t.Errorf("expected ExpireTime 1780000000000, got %d", creds.ExpireTime)
	}
	if !creds.IsQoder {
		t.Errorf("expected IsQoder=true for qoderDir")
	}
}
