//go:build darwin

package updater

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunHelperReplacesAndConfirmsMacApp(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Lingma Tap.app")
	staged := filepath.Join(root, "Lingma Tap.app.new")
	writeFakeMacApp(t, target, "#!/bin/sh\nexit 0\n")
	writeFakeMacApp(t, staged, "#!/bin/sh\nif [ \"$1\" = \"--lingma-update-ack\" ]; then printf 'ok\\n' > \"$2\"; fi\nsleep 1\n")
	transaction := testTransaction(root, target, staged)
	writeTransaction(t, transaction)

	previousLogWriter := log.Writer()
	t.Cleanup(func() { log.SetOutput(previousLogWriter) })
	if err := RunHelper(filepath.Join(root, "transaction.json")); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(target, "Contents", "MacOS", "TestApp"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installed), "lingma-update-ack") {
		t.Fatalf("new application was not installed: %s", installed)
	}
	if _, err := os.Stat(transaction.Backup); !os.IsNotExist(err) {
		t.Fatalf("backup was not removed: %v", err)
	}
}

func TestRunHelperRollsBackMacAppWhenStartupFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Lingma Tap.app")
	staged := filepath.Join(root, "Lingma Tap.app.new")
	restartedMarker := filepath.Join(root, "old-restarted")
	writeFakeMacApp(t, target, fmt.Sprintf("#!/bin/sh\nprintf 'ok\\n' > %q\n", restartedMarker))
	writeFakeMacApp(t, staged, "#!/bin/sh\nexit 1\n")
	transaction := testTransaction(root, target, staged)
	writeTransaction(t, transaction)

	previousLogWriter := log.Writer()
	t.Cleanup(func() { log.SetOutput(previousLogWriter) })
	if err := RunHelper(filepath.Join(root, "transaction.json")); err == nil {
		t.Fatal("expected startup failure")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(restartedMarker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("previous application was not restored and restarted")
}

func testTransaction(root, target, staged string) *Transaction {
	return &Transaction{
		Version: "v1.2.3", ParentPID: 1 << 30,
		Target: target, Staged: staged, Backup: target + ".backup",
		AckPath: filepath.Join(root, "startup.ok"), ArchivePath: filepath.Join(root, "update.zip"),
		LogPath: filepath.Join(root, "helper.log"),
	}
}

func writeTransaction(t *testing.T, transaction *Transaction) {
	t.Helper()
	bytes := []byte(fmt.Sprintf(`{"version":%q,"parent_pid":%d,"target":%q,"staged":%q,"backup":%q,"ack_path":%q,"archive_path":%q,"log_path":%q}`,
		transaction.Version, transaction.ParentPID, transaction.Target, transaction.Staged, transaction.Backup,
		transaction.AckPath, transaction.ArchivePath, transaction.LogPath))
	if err := os.WriteFile(filepath.Join(filepath.Dir(transaction.AckPath), "transaction.json"), bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transaction.ArchivePath, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFakeMacApp(t *testing.T, path, script string) {
	t.Helper()
	macOSDir := filepath.Join(path, "Contents", "MacOS")
	if err := os.MkdirAll(macOSDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>TestApp</string>
<key>CFBundleIdentifier</key><string>com.coolxll.lingma-tap</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(path, "Contents", "Info.plist"), []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(macOSDir, "TestApp")
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}
