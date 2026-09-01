package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestIsReleaseVersion(t *testing.T) {
	tests := map[string]bool{
		"v1.2.3":        true,
		"v0.1.26":       true,
		"1.2.3":         false,
		"v1.2.3-beta.1": false,
		"v1.2.3+build":  false,
		"dev":           false,
	}
	for version, want := range tests {
		if got := IsReleaseVersion(version); got != want {
			t.Errorf("IsReleaseVersion(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestClientCheckAndDownload(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("signed application archive")
	digest := sha256.Sum256(payload)
	manifestBytes, err := json.Marshal(Manifest{
		Version: "v1.2.0",
		Assets: []ManifestAsset{{
			GOOS: "windows", GOARCH: "amd64", Name: "lingma-tap-windows-x64.zip",
			Size: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, manifestBytes)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/coolxll/lingma-tap/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{
				TagName: "v1.2.0", HTMLURL: server.URL + "/release",
				Assets: []githubAsset{
					{Name: ManifestName, BrowserDownloadURL: server.URL + "/manifest", Size: int64(len(manifestBytes))},
					{Name: SignatureName, BrowserDownloadURL: server.URL + "/signature", Size: int64(len(signature))},
					{Name: "lingma-tap-windows-x64.zip", BrowserDownloadURL: server.URL + "/archive", Size: int64(len(payload)), Digest: "sha256:" + hex.EncodeToString(digest[:])},
				},
			})
		case "/manifest":
			_, _ = w.Write(manifestBytes)
		case "/signature":
			_, _ = w.Write(signature)
		case "/archive":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(publicKey)
	client.BaseURL = server.URL
	candidate, err := client.Check(context.Background(), "v1.1.0", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !candidate.Available || candidate.LatestVersion != "v1.2.0" {
		t.Fatalf("unexpected candidate: %+v", candidate)
	}
	destination := filepath.Join(t.TempDir(), candidate.AssetName)
	if err := os.WriteFile(destination, []byte("stale cached archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	var finalProgress int64
	if err := client.Download(context.Background(), candidate, destination, func(downloaded, total int64) {
		finalProgress = downloaded
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) || finalProgress != int64(len(payload)) {
		t.Fatalf("download mismatch: bytes=%q progress=%d", got, finalProgress)
	}
}

func TestClientRejectsInvalidManifestSignature(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/coolxll/lingma-tap/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{
				TagName: "v1.2.0",
				Assets: []githubAsset{
					{Name: ManifestName, BrowserDownloadURL: "http://" + r.Host + "/manifest"},
					{Name: SignatureName, BrowserDownloadURL: "http://" + r.Host + "/signature"},
				},
			})
		case "/manifest":
			_, _ = w.Write([]byte(`{"version":"v1.2.0","assets":[]}`))
		case "/signature":
			_, _ = w.Write(make([]byte, ed25519.SignatureSize))
		}
	}))
	defer server.Close()
	client := NewClient(publicKey)
	client.BaseURL = server.URL
	if _, err := client.Check(context.Background(), "v1.1.0", "windows", "amd64"); err == nil {
		t.Fatal("expected invalid signature error")
	}
}

func TestClientRejectsDevelopmentBuildWithoutNetwork(t *testing.T) {
	client := NewClient(make(ed25519.PublicKey, ed25519.PublicKeySize))
	client.BaseURL = "http://127.0.0.1:1"
	candidate, err := client.Check(context.Background(), "dev", "darwin", "arm64")
	if err != ErrUnsupported {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
	if candidate.Supported {
		t.Fatal("development build must not support automatic updates")
	}
}
