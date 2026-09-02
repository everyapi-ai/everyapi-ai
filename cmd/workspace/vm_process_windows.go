//go:build windows

package workspace

import (
	"context"
	"os"
	"os/exec"
	"strconv"
)

func newVMRecipeCommand(ctx context.Context, command, repoPath string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", command)
	cmd.Dir = repoPath
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return exec.Command("taskkill", "/pid", strconv.Itoa(cmd.Process.Pid), "/t", "/f").Run()
	}
	return cmd
}

func vmManualCleanupCommand(payloadBase64, destroyCommand string) string {
	return `powershell.exe -NoProfile -NonInteractive -Command "[Console]::Out.Write([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + payloadBase64 + `')))" | ` + destroyCommand
}
