package updater

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSignedManifest(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	directory := t.TempDir()
	for _, asset := range releaseAssets {
		if err := os.WriteFile(filepath.Join(directory, asset.Name), []byte(asset.Name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := generateSignedManifest(directory, "v2.0.0", encodedKey, publicKey); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(directory, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join(directory, SignatureName))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(publicKey, manifestBytes, signature) {
		t.Fatal("generated manifest signature is invalid")
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "v2.0.0" || len(manifest.Assets) != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
}

func TestGenerateSignedManifestRejectsWrongKey(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	otherPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(privateKey)
	encodedKey := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err := generateSignedManifest(t.TempDir(), "v2.0.0", encodedKey, otherPublicKey); err == nil {
		t.Fatal("expected public key mismatch")
	}
}
