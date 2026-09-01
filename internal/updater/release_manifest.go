package updater

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var releaseAssets = []ManifestAsset{
	{GOOS: "windows", GOARCH: "amd64", Name: "lingma-tap-windows-x64.zip"},
	{GOOS: "darwin", GOARCH: "amd64", Name: "lingma-tap-macos-x64.zip"},
	{GOOS: "darwin", GOARCH: "arm64", Name: "lingma-tap-macos-arm64.zip"},
}

func GenerateSignedManifest(directory, version, encodedPrivateKey string) error {
	wantPublicKey, err := base64.StdEncoding.DecodeString(PublicKeyBase64)
	if err != nil {
		return err
	}
	return generateSignedManifest(directory, version, encodedPrivateKey, ed25519.PublicKey(wantPublicKey))
}

func generateSignedManifest(directory, version, encodedPrivateKey string, wantPublicKey ed25519.PublicKey) error {
	if !IsReleaseVersion(version) {
		return fmt.Errorf("version must be a stable semantic version with a v prefix")
	}
	privateKey, err := parsePrivateKey(encodedPrivateKey)
	if err != nil {
		return err
	}
	actualPublicKey := privateKey.Public().(ed25519.PublicKey)
	if !actualPublicKey.Equal(wantPublicKey) {
		return fmt.Errorf("UPDATE_SIGNING_PRIVATE_KEY does not match the public key embedded in the application")
	}

	manifest := Manifest{Version: version, Assets: make([]ManifestAsset, 0, len(releaseAssets))}
	for _, asset := range releaseAssets {
		path := filepath.Join(directory, asset.Name)
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open release asset %s: %w", asset.Name, err)
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if size <= 0 {
			return fmt.Errorf("release asset %s is empty", asset.Name)
		}
		asset.Size = size
		asset.SHA256 = hex.EncodeToString(hash.Sum(nil))
		manifest.Assets = append(manifest.Assets, asset)
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestBytes = append(manifestBytes, '\n')
	signature := ed25519.Sign(privateKey, manifestBytes)
	if err := os.WriteFile(filepath.Join(directory, ManifestName), manifestBytes, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, SignatureName), signature, 0o644)
}

func parsePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	if encoded == "" {
		return nil, fmt.Errorf("UPDATE_SIGNING_PRIVATE_KEY is required")
	}
	pemBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode UPDATE_SIGNING_PRIVATE_KEY: %w", err)
	}
	block, rest := pem.Decode(pemBytes)
	if block == nil || len(rest) != 0 {
		return nil, fmt.Errorf("UPDATE_SIGNING_PRIVATE_KEY is not a single PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse UPDATE_SIGNING_PRIVATE_KEY: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("UPDATE_SIGNING_PRIVATE_KEY is not an Ed25519 key")
	}
	return privateKey, nil
}
