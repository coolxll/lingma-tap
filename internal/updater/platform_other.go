//go:build !darwin && !windows

package updater

import (
	"os/exec"
	"time"
)

func helperFilename() string { return "lingma-tap-update-helper" }

func stagePlatformUpdate(candidate *Candidate, archivePath, updateDir, token string) (string, string, error) {
	return "", "", ErrUnsupported
}

func startDetachedHelper(helperPath, transactionPath, logPath string) error { return ErrUnsupported }

func waitForProcessExit(pid int, timeout time.Duration) error { return ErrUnsupported }

func startApplication(target, ackPath, logPath string) (*exec.Cmd, error) { return nil, ErrUnsupported }
