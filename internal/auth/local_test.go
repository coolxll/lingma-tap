package auth

import (
	"fmt"
	"os"
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

	fmt.Printf("MachineID: %s\n", creds.MachineID)
	fmt.Printf("UID: %s\n", creds.UID)
	fmt.Printf("OrgID: %s\n", creds.OrganizationID)
	fmt.Printf("UserType: %s\n", creds.UserType)
	fmt.Printf("Name: %s\n", creds.Name)
	fmt.Printf("CosyKey: %s...\n", creds.CosyKey[:40])
	fmt.Printf("EncryptUserInfo: %s...\n", creds.EncryptUserInfo[:40])

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
	cosyDate := fmt.Sprintf("%d", time.Now().Unix())
	bearer, err := sess.BuildBearer("test-body", "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?Encode=1", cosyDate)
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("Date: %s\n", cosyDate)
	fmt.Printf("Bearer: %s...\n", bearer[:80])

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
