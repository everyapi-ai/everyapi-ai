//go:build windows

package proxy

import (
	"os"
	"os/exec"
	"syscall"
)

// startDetachedCommand on Windows uses CREATE_NEW_PROCESS_GROUP +
// DETACHED_PROCESS so the child doesn't share the console with the
// parent. Without these flags, killing the parent terminal would
// kill the proxy too.
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
