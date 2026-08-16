package auth

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// OAuthSession is the small lifecycle surface shared by the desktop app and
// the standalone oauth-probe command.
type OAuthSession interface {
	Wait() (OAuthResult, error)
	Close() error
}

// OAuthSessionFactory is injectable so the Wails app can be tested without
// starting an external Lingma process.
type OAuthSessionFactory func(context.Context, OAuthOptions, string) (OAuthSession, OAuthStartInfo, error)

// OAuthController owns one browser login and runs completion work away from
// the Wails RPC goroutine.
type OAuthController struct {
	mu      sync.Mutex
	factory OAuthSessionFactory
	current *oauthTask
	nextID  uint64
	status  OAuthLoginStatus
}

type oauthTask struct {
	id      uint64
	session OAuthSession
}

func NewOAuthController(factory ...OAuthSessionFactory) *OAuthController {
	selected := OAuthSessionFactory(func(ctx context.Context, options OAuthOptions, binaryPath string) (OAuthSession, OAuthStartInfo, error) {
		return StartLingmaOfficialOAuthSession(ctx, options, binaryPath)
	})
	if len(factory) > 0 && factory[0] != nil {
		selected = factory[0]
	}
	return &OAuthController{factory: selected}
}

// Start generates a one-time URL and asynchronously waits for official
// Lingma to finish the callback and write temporary credentials.
func (c *OAuthController) Start(ctx context.Context, options OAuthOptions, binaryPath string, onComplete func(*Credentials) error) (string, error) {
	if c == nil {
		return "", fmt.Errorf("OAuth controller is nil")
	}
	if onComplete == nil {
		return "", fmt.Errorf("OAuth completion callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(options.ListenAddr) == "" {
		options.ListenAddr = "127.0.0.1:0"
	}

	c.mu.Lock()
	if c.current != nil {
		c.mu.Unlock()
		return "", fmt.Errorf("OAuth login is already in progress")
	}
	c.nextID++
	task := &oauthTask{id: c.nextID}
	c.current = task
	c.status = OAuthLoginStatus{InProgress: true}
	factory := c.factory
	c.mu.Unlock()

	session, info, err := factory(ctx, options, binaryPath)
	if err != nil {
		c.finishStartFailure(task.id, err)
		return "", err
	}
	if session == nil {
		c.finishStartFailure(task.id, fmt.Errorf("OAuth session is nil"))
		return "", fmt.Errorf("OAuth session is nil")
	}

	c.mu.Lock()
	if c.current == nil || c.current.id != task.id {
		c.mu.Unlock()
		_ = session.Close()
		return "", fmt.Errorf("OAuth login was cancelled")
	}
	task.session = session
	c.status.LoginURL = info.LoginURL
	c.status.ExpiresAt = info.ExpiresAt
	c.mu.Unlock()

	go c.await(task, onComplete)
	return info.LoginURL, nil
}

func (c *OAuthController) await(task *oauthTask, onComplete func(*Credentials) error) {
	defer task.session.Close()
	result, err := task.session.Wait()
	if err == nil {
		err = onComplete(result.Credentials)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil || c.current.id != task.id {
		return
	}
	c.current = nil
	c.status.InProgress = false
	c.status.LoginURL = ""
	if err != nil {
		c.status.Error = redactOAuthError(err)
	} else {
		c.status.Error = ""
	}
}

func (c *OAuthController) finishStartFailure(id uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil || c.current.id != id {
		return
	}
	c.current = nil
	c.status.InProgress = false
	c.status.LoginURL = ""
	c.status.ExpiresAt = time.Time{}
	c.status.Error = redactOAuthError(err)
}

// Status returns display-safe state for the desktop UI.
func (c *OAuthController) Status() OAuthLoginStatus {
	if c == nil {
		return OAuthLoginStatus{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Cancel stops a pending login and records a user-visible cancellation.
func (c *OAuthController) Cancel() {
	c.stop("OAuth login cancelled")
}

// Close stops a pending login without showing a cancellation error during
// normal application shutdown.
func (c *OAuthController) Close() {
	c.stop("")
}

func (c *OAuthController) stop(message string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	task := c.current
	c.current = nil
	c.nextID++
	c.status.InProgress = false
	c.status.LoginURL = ""
	c.status.ExpiresAt = time.Time{}
	c.status.Error = message
	c.mu.Unlock()
	if task != nil && task.session != nil {
		_ = task.session.Close()
	}
}

var oauthSecretPattern = regexp.MustCompile(`(?i)(auth|token|authorization|state|nonce|challenge|machine_id)=([^&\s]+)`)

func redactOAuthError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	message = oauthSecretPattern.ReplaceAllString(message, "$1=<redacted>")
	message = strings.ReplaceAll(message, "\n", " ")
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}
