//go:build !windows

package auth

import (
	"os"
	"os/exec"
)

func configureLingmaCommand(_ *exec.Cmd) {}

func requestLingmaProcessStop(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
}
