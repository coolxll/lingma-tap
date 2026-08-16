package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLingmaBinaryResolverSelectsNewestCompatibleVersion(t *testing.T) {
	home := t.TempDir()
	oldPath := filepath.Join(home, ".lingma", "bin", "v1.9.0", "x86_64_windows", "Lingma.exe")
	newPath := filepath.Join(home, ".lingma", "bin", "v1.10.0", "x86_64_windows", "Lingma.exe")
	for _, path := range []string{oldPath, newPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("binary"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	resolver := LingmaBinaryResolver{
		GOOS:    "windows",
		GOARCH:  "amd64",
		HomeDir: home,
		Env:     func(string) string { return "" },
		LookPath: func(string) (string, error) {
			return "", os.ErrNotExist
		},
	}
	got, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got != newPath {
		t.Fatalf("selected %q, want %q", got, newPath)
	}
}

func TestLingmaBinaryResolverUsesConfiguredPathFirst(t *testing.T) {
	home := t.TempDir()
	configured := filepath.Join(home, "custom", "Lingma.exe")
	if err := os.MkdirAll(filepath.Dir(configured), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configured, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}

	resolver := LingmaBinaryResolver{
		GOOS:    "windows",
		GOARCH:  "amd64",
		HomeDir: home,
		Env: func(key string) string {
			if key == "LINGMA_SERVICE_BINARY" {
				return configured
			}
			return ""
		},
	}
	got, err := resolver.Resolve()
	if err != nil || got != configured {
		t.Fatalf("Resolve = %q, %v; want configured path", got, err)
	}
}

func TestLingmaBinaryResolverRejectsUnsupportedPlatform(t *testing.T) {
	_, err := (LingmaBinaryResolver{GOOS: "linux", GOARCH: "amd64"}).Resolve()
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error = %v, want unsupported platform", err)
	}
}

func TestLingmaBinaryResolverRequiresExecutableOnDarwin(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".lingma", "bin", "v1.0.0", "aarch64_darwin", "Lingma")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := LingmaBinaryResolver{
		GOOS:    "darwin",
		GOARCH:  "arm64",
		HomeDir: home,
		Env:     func(string) string { return "" },
		Stat: func(candidate string) (os.FileInfo, error) {
			if candidate != path {
				return nil, os.ErrNotExist
			}
			return os.Stat(candidate)
		},
		LookPath: func(string) (string, error) {
			return "", os.ErrNotExist
		},
	}
	if _, err := resolver.Resolve(); err == nil {
		t.Fatal("expected non-executable Darwin binary to be rejected")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := resolver.Resolve(); err != nil || got != path {
		t.Fatalf("Resolve = %q, %v after chmod; want %q", got, err, path)
	}
}
