//go:build !windows

package tools

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Exec replaces the current process with the target tool. On Unix
// this is a true `execve` so the child inherits stdin/stdout/stderr
// natively and parent shells see the child's exit code as if `relaya
// use` never existed. Returns only on failure; success "returns" by
// not returning.
func Exec(t *Tool, env map[string]string) error {
	path, err := exec.LookPath(t.ExecName)
	if err != nil {
		return &ErrToolNotFound{Tool: t}
	}
	merged := mergeEnv(env)
	if err := syscall.Exec(path, []string{t.ExecName}, merged); err != nil {
		return fmt.Errorf("exec %s: %w", t.ExecName, err)
	}
	return nil // unreachable
}
