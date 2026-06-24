package tools

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrToolNotFound is returned by Exec when the tool's executable
// isn't on $PATH. cmd/use renders the InstallHint via Error().
type ErrToolNotFound struct {
	Tool *Tool
}

func (e *ErrToolNotFound) Error() string {
	return fmt.Sprintf("%s is not installed.\n  %s", e.Tool.ExecName, e.Tool.InstallHint)
}

// mergeEnv overlays `set` onto os.Environ(): each key in `set`
// replaces the existing var with the same name; keys not in `set`
// pass through unchanged. Returns a fresh []string in KEY=VAL form
// suitable for exec.Command's Env.
//
// Why build an explicit slice rather than os.Setenv + reuse os.Environ():
// the child process only sees the env we hand exec.Command, so passing
// the merged slice is the correct, side-effect-free contract on both
// Unix and Windows.
func mergeEnv(set map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(set))
	overlaid := make(map[string]struct{}, len(set))
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if v, ok := set[k]; ok {
			out = append(out, k+"="+v)
			overlaid[k] = struct{}{}
			continue
		}
		out = append(out, kv)
	}
	for k, v := range set {
		if _, done := overlaid[k]; done {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}

// exitCodeFromWait maps a cmd.Wait() error to the process exit code we
// should propagate: 0 on clean exit, the child's own code on a normal
// non-zero exit, and 1 for anything we can't classify. Shared by the
// Unix and Windows launchers so both platforms agree on the common
// cases; the Unix launcher layers signal-death mapping (128+signo) on
// top before falling through to this (see exec_unix.go).
func exitCodeFromWait(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		return ee.ExitCode()
	}
	return 1
}
