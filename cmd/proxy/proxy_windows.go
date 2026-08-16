//go:build windows

package proxy

import (
	"os"
	"os/exec"
	"syscall"
)

// startDetachedCommand on Windows uses CREATE_NEW_PROCESS_GROUP + DETACHED_PROCESS so the child doesn't share the console with the parent. Without these flags, killing the parent terminal would kill the proxy too.
func startDetachedCommand(exe string, args []string, logFile *os.File) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	return cmd
}

// terminateProcess stops the proxy on Windows. (*os.Process).Signal only supports os.Kill there — SIGTERM returns "not supported by windows" — so we use Kill(), which maps to TerminateProcess. There's no graceful SIGTERM equivalent for a detached console process on Windows.
func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}
