package auth

import (
	"fmt"
	"testing"
)

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
	bearer, date, err := sess.BuildBearer("test-body", "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?Encode=1")
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("Date: %s\n", date)
	fmt.Printf("Bearer: %s...\n", bearer[:80])

	if bearer == "" {
		t.Error("empty bearer")
	}
}
