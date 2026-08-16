package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/bridge"
)

type oauthWaiter interface {
	Wait() (auth.OAuthResult, error)
	Close() error
}

type probeDependencies struct {
	startSession func(context.Context, auth.OAuthOptions, string) (oauthWaiter, auth.OAuthStartInfo, error)
	openBrowser  func(string) error
	fetchModels  func(context.Context, *auth.Credentials) ([]bridge.ModelInfo, error)
}

var (
	queryValuePattern = regexp.MustCompile(`(?i)(auth|token|authorization|machine_id|state|nonce|challenge)=([^&\s]+)`)
	bearerPattern     = regexp.MustCompile(`(?i)Bearer\s+[^\s]+`)
	oauthTokenPattern = regexp.MustCompile(`\b(?:pt|rt)-[A-Za-z0-9._~-]+`)
	cosyTokenPattern  = regexp.MustCompile(`\bCOSY\.[A-Za-z0-9._~+/-]+`)
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, defaultDependencies()))
}

func defaultDependencies() probeDependencies {
	return probeDependencies{
		startSession: func(ctx context.Context, options auth.OAuthOptions, binaryPath string) (oauthWaiter, auth.OAuthStartInfo, error) {
			return auth.StartLingmaOfficialOAuthSession(ctx, options, binaryPath)
		},
		openBrowser: openExternal,
		fetchModels: func(ctx context.Context, credentials *auth.Credentials) ([]bridge.ModelInfo, error) {
			client := bridge.NewLingmaClient(auth.NewSession(credentials))
			return client.FetchModels(ctx)
		},
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, deps probeDependencies) int {
	flags := flag.NewFlagSet("oauth-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listenAddr := flags.String("listen", "127.0.0.1:37510", "loopback callback address; use 127.0.0.1:0 for a random port")
	timeout := flags.Duration("timeout", 5*time.Minute, "maximum time to wait for browser authentication")
	openBrowser := flags.Bool("open", true, "open the login URL in the default browser")
	lingmaBinary := flags.String("lingma-bin", "", "path to the Lingma service binary; auto-detected when empty")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "oauth-probe does not accept positional arguments")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "--timeout must be greater than zero")
		return 2
	}

	session, startInfo, err := deps.startSession(ctx, auth.OAuthOptions{
		ListenAddr: *listenAddr,
		Timeout:    *timeout,
	}, *lingmaBinary)
	if err != nil {
		writeStageError(stderr, "start", err)
		return 1
	}
	defer session.Close()
	fmt.Fprintln(stdout, "stage=start status=ok auth_backend=official_lingma isolated=true")

	fmt.Fprintf(stdout, "callback_listener=%s expires_at=%s\n", startInfo.CallbackAddress, startInfo.ExpiresAt.Format(time.RFC3339))
	fmt.Fprintln(stdout, "Open this one-time login URL:")
	fmt.Fprintln(stdout, startInfo.LoginURL)

	if *openBrowser {
		if err := deps.openBrowser(startInfo.LoginURL); err != nil {
			fmt.Fprintf(stderr, "stage=browser status=warning error=%s\n", safeError(err))
		} else {
			fmt.Fprintln(stdout, "stage=browser status=ok")
		}
	}
	fmt.Fprintln(stdout, "stage=callback status=waiting")

	result, err := session.Wait()
	if err != nil {
		writeStageError(stderr, classifyOAuthError(err), err)
		return 1
	}
	fmt.Fprintln(stdout, "stage=callback status=ok handler=official_lingma")

	uidPresent := strings.TrimSpace(result.Callback.UID) != ""
	namePresent := strings.TrimSpace(result.Callback.Name) != ""
	if !uidPresent || !namePresent {
		writeStageError(stderr, "identity", fmt.Errorf("OAuth callback identity is incomplete"))
		return 1
	}
	fmt.Fprintf(stdout, "stage=identity status=ok uid_present=%t name_present=%t\n", uidPresent, namePresent)

	exchangedCredentials := result.Credentials
	if exchangedCredentials == nil || exchangedCredentials.CosyKey == "" || exchangedCredentials.EncryptUserInfo == "" {
		writeStageError(stderr, "credential_exchange", fmt.Errorf("exchanged Lingma credentials are incomplete"))
		return 1
	}
	fmt.Fprintf(stdout, "stage=credential_exchange status=ok organization_present=%t\n", strings.TrimSpace(exchangedCredentials.OrganizationID) != "")

	models, err := deps.fetchModels(ctx, exchangedCredentials)
	if err != nil {
		writeStageError(stderr, "model_verify", err)
		return 1
	}
	if len(models) == 0 {
		writeStageError(stderr, "model_verify", fmt.Errorf("Lingma returned an empty chat model list"))
		return 1
	}
	fmt.Fprintf(stdout, "stage=model_verify status=ok model_count=%d\n", len(models))
	fmt.Fprintln(stdout, "oauth_probe=passed credentials_persisted=false")
	return 0
}

func classifyOAuthError(err error) string {
	if err == nil {
		return "callback"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "parse oauth callback"):
		return "parse"
	case strings.Contains(message, "build oauth credentials"):
		return "credential_build"
	default:
		return "callback"
	}
}

func writeStageError(w io.Writer, stage string, err error) {
	fmt.Fprintf(w, "stage=%s status=failed error=%s\n", stage, safeError(err))
}

func safeError(err error) string {
	if err == nil {
		return "unknown error"
	}
	message := err.Error()
	message = queryValuePattern.ReplaceAllString(message, "$1=<redacted>")
	message = bearerPattern.ReplaceAllString(message, "Bearer <redacted>")
	message = oauthTokenPattern.ReplaceAllString(message, "oauth-token-<redacted>")
	message = cosyTokenPattern.ReplaceAllString(message, "COSY.<redacted>")
	return strings.ReplaceAll(message, "\n", " ")
}

func openExternal(rawURL string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", rawURL)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		command = exec.Command("xdg-open", rawURL)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}
