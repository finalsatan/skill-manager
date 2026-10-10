//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// detach lets the child outlive the terminal that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
