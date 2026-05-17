//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
)

// Exec on Windows can't execve, so we spawn + wait + propagate exit
// code. The cost is the extra `relaya` process hangs around as a
// parent until the tool exits; the gain is signal handling Just
// Works (the child catches Ctrl+C; we wait for it; we exit with its
// code).
func Exec(t *Tool, env map[string]string) error {
	path, err := exec.LookPath(t.ExecName)
	if err != nil {
		return &ErrToolNotFound{Tool: t}
	}
	cmd := exec.Command(path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergeEnv(env)
	if err := cmd.Run(); err != nil {
		// Surface the child's exit code if it produced one; otherwise
		// re-wrap. exec.ExitError carries ProcessState.ExitCode().
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("run %s: %w", t.ExecName, err)
	}
	os.Exit(0)
	return nil // unreachable
}
