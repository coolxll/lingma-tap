//go:build windows

package updater

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func helperFilename() string { return "lingma-tap-update-helper.exe" }

func stagePlatformUpdate(candidate *Candidate, archivePath, updateDir, token string) (string, string, error) {
	target, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	if err := ensureWritableParent(target); err != nil {
		return "", "", err
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", "", err
	}
	defer reader.Close()
	if len(reader.File) != 1 || reader.File[0].FileInfo().IsDir() || filepath.Base(reader.File[0].Name) != reader.File[0].Name || !strings.HasSuffix(strings.ToLower(reader.File[0].Name), ".exe") {
		return "", "", fmt.Errorf("Windows update archive must contain exactly one executable")
	}
	staged := target + ".new-" + token + ".exe"
	_ = os.Remove(staged)
	in, err := reader.File[0].Open()
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", "", err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(staged)
		}
	}()
	written, err := io.Copy(out, in)
	if err != nil {
		return "", "", fmt.Errorf("extract Windows update: %w", err)
	}
	if written != reader.File[0].FileInfo().Size() {
		return "", "", fmt.Errorf("extracted Windows update size is invalid")
	}
	if err := out.Sync(); err != nil {
		return "", "", err
	}
	if err := out.Close(); err != nil {
		return "", "", err
	}
	ok = true
	return target, staged, nil
}

func startDetachedHelper(helperPath, transactionPath, logPath string) error {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(helperPath, helperFlag, transactionPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS, HideWindow: true}
	return cmd.Start()
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, uint32(timeout/time.Millisecond))
	if err != nil {
		return err
	}
	if result != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("timed out")
	}
	return nil
}

func startApplication(target, ackPath, logPath string) (*exec.Cmd, error) {
	args := []string{}
	if ackPath != "" {
		args = append(args, ackFlag, ackPath)
	}
	cmd := exec.Command(target, args...)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	_ = logFile.Close()
	return cmd, nil
}
