package auth

import (
	"os"
	"testing"
)

func requireAuthIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("LINGMA_AUTH_INTEGRATION") != "1" {
		t.Skip("set LINGMA_AUTH_INTEGRATION=1 to run tests against a real Lingma account")
	}
}

func TestGrantAuthInfos(t *testing.T) {
	requireAuthIntegration(t)
	creds, err := LoadCredentials()
	if err != nil {
		t.Skipf("auth files not found: %v", err)
	}

	// Test grantAuthInfos (pass CosyKey as encryptedKey for testing)
	err = grantAuthInfos(creds, creds.CosyKey)
	if err != nil {
		t.Errorf("grantAuthInfos failed: %v", err)
	}
}

func TestFetchUserStatus(t *testing.T) {
	requireAuthIntegration(t)
	creds, err := LoadCredentials()
	if err != nil {
		t.Skipf("auth files not found: %v", err)
	}

	fullCreds, err := fetchUserStatus(creds)
	if err != nil {
		t.Errorf("fetchUserStatus failed: %v", err)
	}

	if fullCreds.Name == "" {
		t.Error("empty name in fetched status")
	}
}
