package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStartLingmaOfficialOAuthSessionRejectsNonLoopback(t *testing.T) {
	_, _, err := StartLingmaOfficialOAuthSession(context.Background(), OAuthOptions{
		ListenAddr: "0.0.0.0:37510",
		Timeout:    time.Second,
	}, "/does/not/matter")
	if err == nil || !strings.Contains(err.Error(), "must be loopback") {
		t.Fatalf("error = %v", err)
	}
}

func TestStartLingmaOfficialOAuthSessionRejectsInvalidPort(t *testing.T) {
	_, _, err := StartLingmaOfficialOAuthSession(context.Background(), OAuthOptions{
		ListenAddr: "127.0.0.1:not-a-port",
		Timeout:    time.Second,
	}, "/does/not/matter")
	if err == nil || !strings.Contains(err.Error(), "invalid OAuth callback port") {
		t.Fatalf("error = %v", err)
	}
}
