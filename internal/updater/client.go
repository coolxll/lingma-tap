package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const maxMetadataBytes = 1 << 20

type Client struct {
	Repository string
	BaseURL    string
	HTTPClient *http.Client
	PublicKey  ed25519.PublicKey
}

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	HTMLURL    string        `json:"html_url"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

func NewClient(publicKey ed25519.PublicKey) *Client {
	return &Client{
		Repository: DefaultRepository,
		BaseURL:    "https://api.github.com",
		HTTPClient: &http.Client{Timeout: 10 * time.Minute},
		PublicKey:  append(ed25519.PublicKey(nil), publicKey...),
	}
}

func IsReleaseVersion(version string) bool {
	return semver.IsValid(version) && semver.Prerelease(version) == "" && semver.Build(version) == "" && semver.Canonical(version) == version
}

func (c *Client) Check(ctx context.Context, currentVersion, goos, goarch string) (*Candidate, error) {
	info := Info{CurrentVersion: currentVersion}
	if !IsReleaseVersion(currentVersion) || len(c.PublicKey) != ed25519.PublicKeySize {
		return &Candidate{Info: info}, ErrUnsupported
	}

	release, err := c.latestRelease(ctx)
	if err != nil {
		return nil, err
	}
	if release.Draft || release.Prerelease || !IsReleaseVersion(release.TagName) {
		return nil, fmt.Errorf("latest GitHub release is not a stable semantic version")
	}

	manifestAsset, okManifest := findGitHubAsset(release.Assets, ManifestName)
	signatureAsset, okSignature := findGitHubAsset(release.Assets, SignatureName)
	if !okManifest || !okSignature {
		return nil, ErrNoSignedManifest
	}
	manifestBytes, err := c.fetchLimited(ctx, manifestAsset.BrowserDownloadURL, maxMetadataBytes)
	if err != nil {
		return nil, fmt.Errorf("download update manifest: %w", err)
	}
	signature, err := c.fetchLimited(ctx, signatureAsset.BrowserDownloadURL, ed25519.SignatureSize)
	if err != nil {
		return nil, fmt.Errorf("download update signature: %w", err)
	}
	if len(signature) != ed25519.SignatureSize || !ed25519.Verify(c.PublicKey, manifestBytes, signature) {
		return nil, fmt.Errorf("update manifest signature is invalid")
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse update manifest: %w", err)
	}
	if manifest.Version != release.TagName || !IsReleaseVersion(manifest.Version) {
		return nil, fmt.Errorf("update manifest version does not match release tag")
	}
	manifestEntry, ok := findManifestAsset(manifest.Assets, goos, goarch)
	if !ok {
		return nil, fmt.Errorf("release does not support %s/%s", goos, goarch)
	}
	if manifestEntry.Name == "" || filepath.Base(manifestEntry.Name) != manifestEntry.Name {
		return nil, fmt.Errorf("signed manifest contains an invalid asset name")
	}
	releaseAsset, ok := findGitHubAsset(release.Assets, manifestEntry.Name)
	if !ok {
		return nil, fmt.Errorf("release asset %q is missing", manifestEntry.Name)
	}
	if manifestEntry.Size <= 0 || releaseAsset.Size != manifestEntry.Size {
		return nil, fmt.Errorf("release asset size does not match signed manifest")
	}
	wantDigest, err := normalizeSHA256(manifestEntry.SHA256)
	if err != nil {
		return nil, err
	}
	if releaseAsset.Digest != "" {
		githubDigest, err := normalizeSHA256(releaseAsset.Digest)
		if err != nil || subtle.ConstantTimeCompare([]byte(githubDigest), []byte(wantDigest)) != 1 {
			return nil, fmt.Errorf("GitHub asset digest does not match signed manifest")
		}
	}

	info = Info{
		Supported:      true,
		Available:      semver.Compare(manifest.Version, currentVersion) > 0,
		CurrentVersion: currentVersion,
		LatestVersion:  manifest.Version,
		ReleaseURL:     release.HTMLURL,
		AssetName:      manifestEntry.Name,
	}
	return &Candidate{Info: info, DownloadURL: releaseAsset.BrowserDownloadURL, Size: manifestEntry.Size, SHA256: wantDigest}, nil
}

func (c *Client) Download(ctx context.Context, candidate *Candidate, destination string, progress func(downloaded, total int64)) error {
	if candidate == nil || candidate.DownloadURL == "" || candidate.Size <= 0 {
		return fmt.Errorf("invalid update candidate")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	temporary := destination + ".part"
	_ = os.Remove(temporary)
	f, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		_ = f.Close()
		if !keep {
			_ = os.Remove(temporary)
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate.DownloadURL, nil)
	if err != nil {
		return err
	}
	setGitHubHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download update: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != candidate.Size {
		return fmt.Errorf("download size does not match signed manifest")
	}

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, hash), &progressReader{reader: io.LimitReader(resp.Body, candidate.Size+1), total: candidate.Size, onRead: progress})
	if err != nil {
		return err
	}
	if written != candidate.Size {
		return fmt.Errorf("downloaded %d bytes, expected %d", written, candidate.Size)
	}
	gotDigest := hex.EncodeToString(hash.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(gotDigest), []byte(candidate.SHA256)) != 1 {
		return fmt.Errorf("downloaded update checksum is invalid")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return err
	}
	keep = true
	return nil
}

func (c *Client) latestRelease(ctx context.Context) (*githubRelease, error) {
	url := strings.TrimRight(c.BaseURL, "/") + "/repos/" + c.Repository + "/releases/latest"
	bytes, err := c.fetchLimited(ctx, url, maxMetadataBytes)
	if err != nil {
		return nil, fmt.Errorf("check GitHub release: %w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(bytes, &release); err != nil {
		return nil, fmt.Errorf("parse GitHub release: %w", err)
	}
	return &release, nil
}

func (c *Client) fetchLimited(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	bytes, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(bytes)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return bytes, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func setGitHubHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lingma-tap-updater")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func findGitHubAsset(assets []githubAsset, name string) (githubAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return githubAsset{}, false
}

func findManifestAsset(assets []ManifestAsset, goos, goarch string) (ManifestAsset, bool) {
	for _, asset := range assets {
		if asset.GOOS == goos && asset.GOARCH == goarch {
			return asset, true
		}
	}
	return ManifestAsset{}, false
}

func normalizeSHA256(value string) (string, error) {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("invalid SHA-256 digest")
	}
	return hex.EncodeToString(decoded), nil
}

type progressReader struct {
	reader io.Reader
	total  int64
	read   int64
	onRead func(downloaded, total int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.read += int64(n)
		if r.onRead != nil {
			r.onRead(r.read, r.total)
		}
	}
	return n, err
}
