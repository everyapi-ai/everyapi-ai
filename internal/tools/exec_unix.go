//go:build !windows

package tools

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Exec replaces the current process with the target tool. On Unix
// this is a true `execve` so the child inherits stdin/stdout/stderr
// natively and parent shells see the child's exit code as if `everyapi
// use` never existed. Returns only on failure; success "returns" by
// not returning.
//
// extraArgs are appended to argv after t.ExecName, so callers can
// pass through user-supplied flags (e.g. `--dangerously-skip-permissions`
// for claude). nil is fine.
func Exec(t *Tool, env map[string]string, extraArgs []string) error {
	path, err := exec.LookPath(t.ExecName)
	if err != nil {
		return &ErrToolNotFound{Tool: t}
	}
	merged := mergeEnv(env)
	argv := append([]string{t.ExecName}, extraArgs...)
	if err := syscall.Exec(path, argv, merged); err != nil {
		return fmt.Errorf("exec %s: %w", t.ExecName, err)
	}
	return nil // unreachable
}
