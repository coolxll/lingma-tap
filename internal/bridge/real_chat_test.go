package bridge

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/joho/godotenv"
)

func init() {
	_ = godotenv.Load("../../.env")
}

func TestRealChatStreamWithOAuth(t *testing.T) {
	// 1. Try with Local Credentials First (Baseline)
	fmt.Println(">>> Step 0: Testing with Local Credentials (Baseline)...")
	localCreds, err := auth.LoadCredentials()
	if err != nil {
		t.Logf("Skipping local baseline: %v", err)
	} else {
		fmt.Printf("    Local UID: %s, OrgID: %s\n", localCreds.UID, localCreds.OrganizationID)
		fmt.Printf("    Local Info: %s\n", localCreds.EncryptUserInfo)
		testWithCreds(t, localCreds, "Local")
	}

	// 2. OAuth Exchange (Historical params)
	userId := os.Getenv("LINGMA_UID")
	securityToken := os.Getenv("LINGMA_TOKEN")
	machineID := os.Getenv("LINGMA_MID")

	if userId == "" || securityToken == "" || machineID == "" {
		t.Skip("Skipping OAuth test: LINGMA_UID, LINGMA_TOKEN, or LINGMA_MID not set in environment")
	}

	fmt.Println("\n>>> Step 1: Exchanging OAuth credentials...")
	creds, err := auth.ExchangeCallback(userId, securityToken, machineID)
	if err != nil {
		t.Fatalf("ExchangeCallback failed: %v", err)
	}
	
	// FALLBACK: If OAuth didn't provide info, try borrowing from local (for testing)
	if creds.EncryptUserInfo == "" && localCreds != nil {
		fmt.Println("    [TEST] Borrowing EncryptUserInfo from local baseline...")
		creds.EncryptUserInfo = localCreds.EncryptUserInfo
	}

	fmt.Printf("    OAuth UID: %s, OrgID: %s\n", creds.UID, creds.OrganizationID)

	// 3. Test with Exchanged Credentials
	testWithCreds(t, creds, "OAuth")
}

func testWithCreds(t *testing.T, creds *auth.Credentials, label string) {
	session := auth.NewSession(creds)
	client := NewLingmaClient(session)
	client.Debug = true

	messages := []map[string]any{
		{"role": "user", "content": "Ping! Reply with 'Pong' only."},
	}
	modelKey := "dashscope_qmodel" 
	body := BuildLingmaBody(messages, nil, modelKey, nil)

	fmt.Printf(">>> Step 2 [%s]: Sending ChatStream request...\n", label)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	receivedContent := false
	err := client.ChatStream(ctx, body, func(ev SSEEvent) error {
		if ev.Type == "data" && ev.Content != "" {
			fmt.Printf("    [%s] Received: %s\n", label, ev.Content)
			receivedContent = true
		}
		return nil
	})

	if err != nil {
		t.Errorf("  [%s] ChatStream failed: %v", label, err)
	} else if !receivedContent {
		t.Errorf("  [%s] Failed to receive any content", label)
	} else {
		fmt.Printf(">>> [%s] Success!\n", label)
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
