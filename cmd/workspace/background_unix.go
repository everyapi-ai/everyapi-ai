//go:build !windows

package workspace

import (
	"os/exec"
	"syscall"
)

func detachCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
