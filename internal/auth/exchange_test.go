package auth

import (
	"fmt"
	"testing"
)

func TestGrantAuthInfos(t *testing.T) {
	creds, err := LoadCredentials()
	if err != nil {
		t.Skipf("auth files not found: %v", err)
	}

	fmt.Printf("UID: %s\n", creds.UID)
	fmt.Printf("OrgID: %s\n", creds.OrganizationID)

	// Test grantAuthInfos
	err = grantAuthInfos(creds)
	if err != nil {
		t.Errorf("grantAuthInfos failed: %v", err)
	}
}

func TestFetchUserStatus(t *testing.T) {
	creds, err := LoadCredentials()
	if err != nil {
		t.Skipf("auth files not found: %v", err)
	}

	fullCreds, err := fetchUserStatus(creds)
	if err != nil {
		t.Errorf("fetchUserStatus failed: %v", err)
	}

	fmt.Printf("Fetched Name: %s\n", fullCreds.Name)
	if fullCreds.Name == "" {
		t.Error("empty name in fetched status")
	}
}
