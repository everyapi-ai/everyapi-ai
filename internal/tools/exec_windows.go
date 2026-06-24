//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
)

// Exec on Windows can't execve, so we spawn + wait + propagate exit
// code. The cost is the extra `everyapi` process hangs around as a
// parent until the tool exits; the gain is signal handling Just
// Works (the child catches Ctrl+C; we wait for it; we exit with its
// code). Mirrors the Unix build's Start/Wait structure and shares
// exitCodeFromWait so both platforms classify exits the same way.
//
// extraArgs are passed through as command-line args to the tool, so
// callers can forward user-supplied flags (e.g.
// `--dangerously-skip-permissions` for claude). nil is fine.
func Exec(t *Tool, env map[string]string, extraArgs []string) error {
	path, err := exec.LookPath(t.ExecName)
	if err != nil {
		return &ErrToolNotFound{Tool: t}
	}
	cmd := exec.Command(path, extraArgs...)
	cmd.Args = append([]string{t.ExecName}, extraArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergeEnv(env)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", t.ExecName, err)
	}
	os.Exit(exitCodeFromWait(cmd.Wait()))
	return nil // unreachable
}
