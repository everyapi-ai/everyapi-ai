package tools

import (
	"fmt"
	"os"
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
// suitable for syscall.Exec / exec.Command.
//
// Why we don't `os.Setenv` and reuse os.Environ(): on Unix the
// syscall.Exec child only sees the explicit env arg, not the
// parent's mutated environ — passing the merged slice is the
// correct contract. On Windows the same shape works for exec.Command.
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
