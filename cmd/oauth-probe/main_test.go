package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/bridge"
)

type fakeOAuthWaiter struct {
	result auth.OAuthResult
	err    error
	closed bool
}

func (f *fakeOAuthWaiter) Wait() (auth.OAuthResult, error) {
	return f.result, f.err
}

func (f *fakeOAuthWaiter) Close() error {
	f.closed = true
	return nil
}

func TestRunPassesOnlyAfterIdentityCredentialsAndModels(t *testing.T) {
	exchangedCredentials := &auth.Credentials{
		MachineID:       "machine-id-1234567890",
		CosyKey:         "exchanged-cosy-secret",
		EncryptUserInfo: "exchanged-encrypted-secret",
		OrganizationID:  "organization-secret",
	}
	waiter := &fakeOAuthWaiter{result: auth.OAuthResult{
		Callback: auth.OAuthCallback{
			UID:                "uid-secret-value",
			Name:               "name-secret-value",
			SecurityOAuthToken: "pt-secret-value",
		},
		Credentials: exchangedCredentials,
	}}

	deps := probeDependencies{
		startSession: func(context.Context, auth.OAuthOptions, string) (oauthWaiter, auth.OAuthStartInfo, error) {
			return waiter, auth.OAuthStartInfo{
				LoginURL:        "https://devops.aliyun.com/lingma/login?state=one-time",
				CallbackAddress: "127.0.0.1:37510",
				ExpiresAt:       time.Unix(1_800_000_000, 0),
			}, nil
		},
		openBrowser: func(string) error { return nil },
		fetchModels: func(_ context.Context, got *auth.Credentials) ([]bridge.ModelInfo, error) {
			if got != exchangedCredentials {
				t.Fatalf("fetchModels did not receive exchanged credentials")
			}
			return []bridge.ModelInfo{{Key: "qmodel"}}, nil
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run(context.Background(), []string{"--open=false"}, &stdout, &stderr, deps)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%s", exitCode, stderr.String())
	}
	if !waiter.closed {
		t.Fatal("OAuth session was not closed")
	}
	if !strings.Contains(stdout.String(), "oauth_probe=passed credentials_persisted=false") ||
		!strings.Contains(stdout.String(), "model_count=1") {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
	for _, secret := range []string{"pt-secret-value", "uid-secret-value", "name-secret-value", "exchanged-cosy-secret", "exchanged-encrypted-secret", "organization-secret"} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Fatalf("probe output leaked %q", secret)
		}
	}
}

func TestRunRedactsModelVerificationError(t *testing.T) {
	waiter := &fakeOAuthWaiter{result: auth.OAuthResult{
		Callback: auth.OAuthCallback{UID: "uid", Name: "name"},
		Credentials: &auth.Credentials{
			CosyKey:         "key",
			EncryptUserInfo: "info",
		},
	}}
	deps := probeDependencies{
		startSession: func(context.Context, auth.OAuthOptions, string) (oauthWaiter, auth.OAuthStartInfo, error) {
			return waiter, auth.OAuthStartInfo{LoginURL: "https://example.invalid/login", ExpiresAt: time.Now()}, nil
		},
		openBrowser: func(string) error { return nil },
		fetchModels: func(context.Context, *auth.Credentials) ([]bridge.ModelInfo, error) {
			return nil, fmt.Errorf("GET https://example.invalid/models?token=rt-secret Bearer bearer-secret pt-oauth-secret COSY.cosy-secret")
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run(context.Background(), []string{"--open=false"}, &stdout, &stderr, deps)
	if exitCode != 1 {
		t.Fatalf("exit code = %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "stage=model_verify status=failed") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	for _, secret := range []string{"rt-secret", "bearer-secret", "pt-oauth-secret", "cosy-secret"} {
		if strings.Contains(stderr.String(), secret) {
			t.Fatalf("error output leaked %q: %s", secret, stderr.String())
		}
	}
}

func TestRunRejectsEmptyModelList(t *testing.T) {
	waiter := &fakeOAuthWaiter{result: auth.OAuthResult{
		Callback:    auth.OAuthCallback{UID: "uid", Name: "name"},
		Credentials: &auth.Credentials{CosyKey: "key", EncryptUserInfo: "info"},
	}}
	deps := probeDependencies{
		startSession: func(context.Context, auth.OAuthOptions, string) (oauthWaiter, auth.OAuthStartInfo, error) {
			return waiter, auth.OAuthStartInfo{LoginURL: "https://example.invalid/login", ExpiresAt: time.Now()}, nil
		},
		openBrowser: func(string) error { return nil },
		fetchModels: func(context.Context, *auth.Credentials) ([]bridge.ModelInfo, error) {
			return nil, nil
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run(context.Background(), []string{"--open=false"}, &stdout, &stderr, deps); exitCode != 1 {
		t.Fatalf("exit code = %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "empty chat model list") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}
