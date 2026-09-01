package main

import (
	"crypto/ed25519"
	"testing"
)

func TestUpdatePublicKey(t *testing.T) {
	if got := updatePublicKey(); len(got) != ed25519.PublicKeySize {
		t.Fatalf("public key length = %d, want %d", len(got), ed25519.PublicKeySize)
	}
}

func TestCheckForUpdateSkipsNonReleaseBuild(t *testing.T) {
	app := NewApp()
	originalVersion := Version
	Version = "development-build"
	t.Cleanup(func() { Version = originalVersion })
	info, err := app.CheckForUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if info.Supported || info.Available {
		t.Fatalf("unexpected update info: %+v", info)
	}
}

func TestInstallUpdateRequiresCheckedCandidate(t *testing.T) {
	app := NewApp()
	if err := app.InstallUpdate(); err == nil {
		t.Fatal("expected InstallUpdate to reject a missing candidate")
	}
}
