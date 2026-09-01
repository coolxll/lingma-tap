//go:build darwin

package updater

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func helperFilename() string { return "lingma-tap-update-helper" }

func stagePlatformUpdate(candidate *Candidate, archivePath, updateDir, token string) (string, string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	macOSDir := filepath.Dir(executable)
	contentsDir := filepath.Dir(macOSDir)
	target := filepath.Dir(contentsDir)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(contentsDir) != "Contents" || filepath.Ext(target) != ".app" {
		return "", "", ErrManualRequired
	}
	if err := ensureWritableParent(target); err != nil {
		return "", "", err
	}

	extractDir := filepath.Join(filepath.Dir(target), ".lingma-tap-update-"+token)
	_ = os.RemoveAll(extractDir)
	if err := os.Mkdir(extractDir, 0o700); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrManualRequired, err)
	}
	defer os.RemoveAll(extractDir)
	cmd := exec.Command("/usr/bin/ditto", "-x", "-k", archivePath, extractDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("extract macOS update: %w: %s", err, strings.TrimSpace(string(output)))
	}
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", "", err
	}
	var appPath string
	for _, entry := range entries {
		if entry.IsDir() && filepath.Ext(entry.Name()) == ".app" {
			if appPath != "" {
				return "", "", fmt.Errorf("macOS update contains multiple app bundles")
			}
			appPath = filepath.Join(extractDir, entry.Name())
		}
	}
	if appPath == "" {
		return "", "", fmt.Errorf("macOS update does not contain an app bundle")
	}
	if err := validateMacBundle(appPath, candidate.LatestVersion); err != nil {
		return "", "", err
	}
	staged := target + ".new-" + token
	_ = os.RemoveAll(staged)
	if err := os.Rename(appPath, staged); err != nil {
		return "", "", err
	}
	return target, staged, nil
}

func validateMacBundle(appPath, version string) error {
	plist := filepath.Join(appPath, "Contents", "Info.plist")
	bundleID, err := plistValue(plist, "CFBundleIdentifier")
	if err != nil || bundleID != "com.coolxll.lingma-tap" {
		return fmt.Errorf("macOS update has an unexpected bundle identifier")
	}
	bundleVersion, err := plistValue(plist, "CFBundleShortVersionString")
	if err != nil || bundleVersion != strings.TrimPrefix(version, "v") {
		return fmt.Errorf("macOS update bundle version does not match release")
	}
	executableName, err := plistValue(plist, "CFBundleExecutable")
	if err != nil || executableName == "" {
		return fmt.Errorf("macOS update has no bundle executable")
	}
	binary := filepath.Join(appPath, "Contents", "MacOS", executableName)
	if output, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", "--verbose=2", appPath).CombinedOutput(); err != nil {
		return fmt.Errorf("macOS update code signature is invalid: %w: %s", err, strings.TrimSpace(string(output)))
	}
	archs, err := exec.Command("/usr/bin/lipo", "-archs", binary).Output()
	wantArchitecture := runtime.GOARCH
	if wantArchitecture == "amd64" {
		wantArchitecture = "x86_64"
	}
	if err != nil || !containsWord(string(archs), wantArchitecture) {
		return fmt.Errorf("macOS update architecture does not match this Mac")
	}
	return nil
}

func plistValue(plist, key string) (string, error) {
	output, err := exec.Command("/usr/bin/plutil", "-extract", key, "raw", "-o", "-", plist).Output()
	return strings.TrimSpace(string(output)), err
}

func containsWord(value, want string) bool {
	for _, field := range strings.Fields(value) {
		if field == want {
			return true
		}
	}
	return false
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out")
}

func startApplication(target, ackPath, logPath string) (*exec.Cmd, error) {
	plist := filepath.Join(target, "Contents", "Info.plist")
	executableName, err := plistValue(plist, "CFBundleExecutable")
	if err != nil {
		return nil, err
	}
	args := []string{}
	if ackPath != "" {
		args = append(args, ackFlag, ackPath)
	}
	cmd := exec.Command(filepath.Join(target, "Contents", "MacOS", executableName), args...)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	_ = logFile.Close()
	return cmd, nil
}
