package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type controllerTestSession struct {
	result OAuthResult
	err    error
	closed chan struct{}
}

func (s *controllerTestSession) Wait() (OAuthResult, error) {
	if s.closed != nil {
		<-s.closed
	}
	return s.result, s.err
}

func (s *controllerTestSession) Close() error {
	if s.closed == nil {
		return nil
	}
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

func TestOAuthControllerCompletesAndUpdatesStatus(t *testing.T) {
	completed := make(chan *Credentials, 1)
	controller := NewOAuthController(func(context.Context, OAuthOptions, string) (OAuthSession, OAuthStartInfo, error) {
		return &controllerTestSession{result: OAuthResult{Credentials: &Credentials{UID: "uid"}}}, OAuthStartInfo{
			LoginURL:  "https://devops.aliyun.com/lingma/login?one-time=1",
			ExpiresAt: time.Now().Add(time.Minute),
		}, nil
	})

	url, err := controller.Start(context.Background(), OAuthOptions{}, "Lingma", func(creds *Credentials) error {
		completed <- creds
		return nil
	})
	if err != nil || url == "" {
		t.Fatalf("Start = %q, %v", url, err)
	}
	select {
	case creds := <-completed:
		if creds == nil || creds.UID != "uid" {
			t.Fatalf("unexpected completion credentials: %+v", creds)
		}
	case <-time.After(time.Second):
		t.Fatal("completion callback did not run")
	}
	deadline := time.Now().Add(time.Second)
	for controller.Status().InProgress && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	status := controller.Status()
	if status.InProgress || status.Error != "" || status.LoginURL != "" {
		t.Fatalf("unexpected final status: %+v", status)
	}
}

func TestOAuthControllerCancelStopsSession(t *testing.T) {
	session := &controllerTestSession{closed: make(chan struct{})}
	controller := NewOAuthController(func(context.Context, OAuthOptions, string) (OAuthSession, OAuthStartInfo, error) {
		return session, OAuthStartInfo{LoginURL: "https://devops.aliyun.com/lingma/login?one-time=1"}, nil
	})
	if _, err := controller.Start(context.Background(), OAuthOptions{}, "Lingma", func(*Credentials) error { return nil }); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	controller.Cancel()
	status := controller.Status()
	if status.InProgress || status.Error != "OAuth login cancelled" {
		t.Fatalf("unexpected cancellation status: %+v", status)
	}
	select {
	case <-session.closed:
	default:
		t.Fatal("session was not closed")
	}
}

func TestOAuthControllerReportsCompletionErrorSafely(t *testing.T) {
	controller := NewOAuthController(func(context.Context, OAuthOptions, string) (OAuthSession, OAuthStartInfo, error) {
		return &controllerTestSession{result: OAuthResult{Credentials: &Credentials{UID: "uid"}}}, OAuthStartInfo{LoginURL: "https://devops.aliyun.com/lingma/login?one-time=1"}, nil
	})
	if _, err := controller.Start(context.Background(), OAuthOptions{}, "Lingma", func(*Credentials) error {
		return errors.New("callback failed token=secret-value")
	}); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for controller.Status().InProgress && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	status := controller.Status()
	if status.Error == "" || status.Error == "callback failed token=secret-value" {
		t.Fatalf("unexpected unredacted error: %q", status.Error)
	}
}
