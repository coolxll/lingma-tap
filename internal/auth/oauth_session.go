package auth

import "time"

const (
	defaultOAuthListenAddr = "127.0.0.1:37510"
	oauthLoginTimeout      = 5 * time.Minute
)

// OAuthOptions configures an isolated Lingma OAuth session.
type OAuthOptions struct {
	ListenAddr string
	Timeout    time.Duration
}

// OAuthStartInfo contains the display-safe information needed to complete a
// browser login. LoginURL contains transient state and must not be logged.
type OAuthStartInfo struct {
	LoginURL        string
	CallbackAddress string
	ExpiresAt       time.Time
}

// OAuthResult contains only the decoded identity and official temporary-cache
// credentials. It intentionally contains no raw URL or callback values.
type OAuthResult struct {
	Callback    OAuthCallback
	Credentials *Credentials
}
