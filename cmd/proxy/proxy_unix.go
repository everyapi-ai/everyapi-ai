//go:build !windows

package proxy

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

// terminateProcess asks the proxy to shut down. On Unix that's SIGTERM,
// which the server traps for a graceful shutdown (see the
// signal.NotifyContext in proxyStart). Split per-OS because Windows'
// (*os.Process).Signal rejects SIGTERM — see proxy_windows.go.
func terminateProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}
