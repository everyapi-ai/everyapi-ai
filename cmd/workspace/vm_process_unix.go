//go:build !windows

package workspace

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

func newVMRecipeCommand(ctx context.Context, command, repoPath string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = repoPath
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd
}

func vmManualCleanupCommand(payloadBase64, destroyCommand string) string {
	return "printf '%s' '" + payloadBase64 + "' | base64 --decode | " + destroyCommand
}
