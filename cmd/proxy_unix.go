//go:build !windows

package cmd

import (
	"os"
	"os/exec"
	"syscall"
)

// startDetachedCommand builds an *exec.Cmd that survives the parent's
// exit on Unix systems. The child is placed in its own session
// (Setsid) so closing the parent's controlling terminal doesn't
// deliver SIGHUP to it. stdin is /dev/null; stdout + stderr are tied
// to the log file the caller opened.
func startDetachedCommand(exe string, args []string, logFile *os.File) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	return cmd
}
