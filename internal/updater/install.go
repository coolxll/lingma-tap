package updater

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	helperFlag = "--lingma-update-helper"
	ackFlag    = "--lingma-update-ack"
)

type Transaction struct {
	Version     string `json:"version"`
	ParentPID   int    `json:"parent_pid"`
	Target      string `json:"target"`
	Staged      string `json:"staged"`
	Backup      string `json:"backup"`
	AckPath     string `json:"ack_path"`
	ArchivePath string `json:"archive_path"`
	LogPath     string `json:"log_path"`
}

func HelperInvocation(args []string) (string, bool) {
	return flagValue(args, helperFlag)
}

func AckInvocation(args []string) (string, bool) {
	return flagValue(args, ackFlag)
}

func flagValue(args []string, flag string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] != "" {
			return args[i+1], true
		}
	}
	return "", false
}

func Prepare(candidate *Candidate, archivePath, dataDir string) (string, error) {
	if candidate == nil || !candidate.Available || !IsReleaseVersion(candidate.LatestVersion) {
		return "", fmt.Errorf("no verified update is available")
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	updateDir := filepath.Join(dataDir, "updates", candidate.LatestVersion+"-"+token)
	if err := os.MkdirAll(updateDir, 0o700); err != nil {
		return "", err
	}

	target, staged, err := stagePlatformUpdate(candidate, archivePath, updateDir, token)
	if err != nil {
		return "", err
	}
	launched := false
	defer func() {
		if !launched {
			_ = os.RemoveAll(staged)
			_ = os.RemoveAll(updateDir)
		}
	}()
	currentExecutable, err := os.Executable()
	if err != nil {
		return "", err
	}
	helperPath := filepath.Join(updateDir, helperFilename())
	if err := copyFile(currentExecutable, helperPath, 0o700); err != nil {
		return "", fmt.Errorf("prepare update helper: %w", err)
	}

	transaction := Transaction{
		Version:     candidate.LatestVersion,
		ParentPID:   os.Getpid(),
		Target:      target,
		Staged:      staged,
		Backup:      target + ".backup-" + token,
		AckPath:     filepath.Join(updateDir, "startup.ok"),
		ArchivePath: archivePath,
		LogPath:     filepath.Join(updateDir, "helper.log"),
	}
	transactionPath := filepath.Join(updateDir, "transaction.json")
	transactionBytes, err := json.Marshal(transaction)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(transactionPath, transactionBytes, 0o600); err != nil {
		return "", err
	}
	if err := startDetachedHelper(helperPath, transactionPath, transaction.LogPath); err != nil {
		return "", fmt.Errorf("start update helper: %w", err)
	}
	launched = true
	return transactionPath, nil
}

func RunHelper(transactionPath string) error {
	transactionPath, err := filepath.Abs(transactionPath)
	if err != nil {
		return err
	}
	bytes, err := os.ReadFile(transactionPath)
	if err != nil {
		return err
	}
	var transaction Transaction
	if err := json.Unmarshal(bytes, &transaction); err != nil {
		return err
	}
	if err := validateTransaction(&transaction); err != nil {
		return err
	}

	logFile, err := os.OpenFile(transaction.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		defer logFile.Close()
		log.SetOutput(logFile)
	}
	log.Printf("waiting for process %d to exit", transaction.ParentPID)
	if err := waitForProcessExit(transaction.ParentPID, 45*time.Second); err != nil {
		return fmt.Errorf("wait for application exit: %w", err)
	}

	_ = os.RemoveAll(transaction.Backup)
	if err := os.Rename(transaction.Target, transaction.Backup); err != nil {
		return fmt.Errorf("backup current application: %w", err)
	}
	restored := false
	defer func() {
		if !restored {
			return
		}
		log.Printf("restored previous application")
	}()
	if err := os.Rename(transaction.Staged, transaction.Target); err != nil {
		_ = os.Rename(transaction.Backup, transaction.Target)
		restored = true
		return fmt.Errorf("install update: %w", err)
	}

	_ = os.Remove(transaction.AckPath)
	cmd, err := startApplication(transaction.Target, transaction.AckPath, transaction.LogPath)
	if err != nil {
		rollbackUpdate(&transaction, nil)
		return fmt.Errorf("restart updated application: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			rollbackUpdate(&transaction, nil)
			return fmt.Errorf("updated application exited before startup confirmation: %v", err)
		case <-ticker.C:
			if _, err := os.Stat(transaction.AckPath); err == nil {
				log.Printf("updated application confirmed startup")
				_ = os.RemoveAll(transaction.Backup)
				_ = os.Remove(transaction.ArchivePath)
				_ = os.Remove(transaction.AckPath)
				_ = os.Remove(transactionPath)
				return nil
			}
		case <-deadline.C:
			_ = cmd.Process.Kill()
			<-exited
			rollbackUpdate(&transaction, nil)
			return fmt.Errorf("updated application did not confirm startup")
		}
	}
}

func AcknowledgeStartup(path, dataDir string) error {
	if path == "" {
		return nil
	}
	ackPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	updatesRoot, err := filepath.Abs(filepath.Join(dataDir, "updates"))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(updatesRoot, ackPath)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.Base(ackPath) != "startup.ok" {
		return fmt.Errorf("invalid update startup acknowledgement path")
	}
	if err := os.WriteFile(ackPath, []byte("ok\n"), 0o600); err != nil {
		return err
	}
	go func(directory string) {
		time.Sleep(45 * time.Second)
		_ = os.RemoveAll(directory)
	}(filepath.Dir(ackPath))
	return nil
}

func rollbackUpdate(transaction *Transaction, running *exec.Cmd) {
	if running != nil && running.Process != nil {
		_ = running.Process.Kill()
		_ = running.Wait()
	}
	failedPath := transaction.Target + ".failed"
	_ = os.RemoveAll(failedPath)
	_ = os.Rename(transaction.Target, failedPath)
	if err := os.Rename(transaction.Backup, transaction.Target); err != nil {
		log.Printf("rollback failed: %v", err)
		return
	}
	_ = os.RemoveAll(failedPath)
	cmd, err := startApplication(transaction.Target, "", transaction.LogPath)
	if err != nil {
		log.Printf("restart previous application failed: %v", err)
	} else if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
}

func validateTransaction(transaction *Transaction) error {
	if !IsReleaseVersion(transaction.Version) || transaction.ParentPID <= 0 {
		return fmt.Errorf("invalid update transaction")
	}
	paths := []string{transaction.Target, transaction.Staged, transaction.Backup, transaction.AckPath, transaction.LogPath}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("update transaction contains a relative path")
		}
	}
	if transaction.Target == transaction.Staged || transaction.Target == transaction.Backup || transaction.Staged == transaction.Backup {
		return fmt.Errorf("update transaction paths overlap")
	}
	return nil
}

func ensureWritableParent(target string) error {
	parent := filepath.Dir(target)
	testFile, err := os.CreateTemp(parent, ".lingma-tap-update-write-test-")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrManualRequired, err)
	}
	name := testFile.Name()
	_ = testFile.Close()
	_ = os.Remove(name)
	return nil
}

func copyFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func randomToken() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
