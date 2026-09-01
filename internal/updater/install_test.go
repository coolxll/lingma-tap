package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInvocationFlags(t *testing.T) {
	if value, ok := HelperInvocation([]string{"app", helperFlag, "/tmp/transaction.json"}); !ok || value != "/tmp/transaction.json" {
		t.Fatalf("HelperInvocation = %q, %v", value, ok)
	}
	if value, ok := AckInvocation([]string{ackFlag, "/tmp/startup.ok"}); !ok || value != "/tmp/startup.ok" {
		t.Fatalf("AckInvocation = %q, %v", value, ok)
	}
}

func TestAcknowledgeStartupRequiresUpdatesDirectory(t *testing.T) {
	dataDir := t.TempDir()
	outside := filepath.Join(dataDir, "outside", "startup.ok")
	if err := os.MkdirAll(filepath.Dir(outside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := AcknowledgeStartup(outside, dataDir); err == nil {
		t.Fatal("expected acknowledgement path rejection")
	}
	inside := filepath.Join(dataDir, "updates", "v1.2.3-test", "startup.ok")
	if err := os.MkdirAll(filepath.Dir(inside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := AcknowledgeStartup(inside, dataDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTransactionRejectsOverlappingPaths(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "app")
	transaction := &Transaction{
		Version: "v1.2.3", ParentPID: 1, Target: target, Staged: target,
		Backup: filepath.Join(root, "backup"), AckPath: filepath.Join(root, "ack"), LogPath: filepath.Join(root, "log"),
	}
	if err := validateTransaction(transaction); err == nil {
		t.Fatal("expected overlapping path error")
	}
}
