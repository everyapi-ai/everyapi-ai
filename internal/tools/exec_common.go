package tools

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrToolNotFound is returned by Exec when the tool's executable
// isn't on $PATH. cmd/use renders the InstallHint via Error().
type ErrToolNotFound struct {
	Tool *Tool
}

// ExecOptions describes one supervised tool launch. Cleanup runs after the
// child exits and before the parent propagates its exit code, and also runs if
// launch setup fails. UnsetEnv removes inherited values instead of replacing
// them with observably-present empty strings.
type ExecOptions struct {
	Env      map[string]string
	UnsetEnv []string
	Args     []string
	Cleanup  func()
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
	return mergeEnvRemoving(set, nil)
}

func mergeEnvRemoving(set map[string]string, unset []string) []string {
	out := make([]string, 0, len(os.Environ())+len(set))
	type envValue struct {
		key   string
		value string
	}
	normalize := func(key string) string {
		if runtime.GOOS == "windows" {
			return strings.ToLower(key)
		}
		return key
	}
	setByKey := make(map[string]envValue, len(set))
	for key, value := range set {
		setByKey[normalize(key)] = envValue{key: key, value: value}
	}
	removed := make(map[string]struct{}, len(unset))
	for _, key := range unset {
		removed[normalize(key)] = struct{}{}
	}
	overlaid := make(map[string]struct{}, len(set))
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		normalized := normalize(k)
		if _, drop := removed[normalized]; drop {
			continue
		}
		if v, ok := setByKey[normalized]; ok {
			out = append(out, k+"="+v.value)
			overlaid[normalized] = struct{}{}
			continue
		}
		out = append(out, kv)
	}
	for k, v := range set {
		if _, done := overlaid[normalize(k)]; done {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}

// withExecDirOnPath returns env with the directory of execPath APPENDED
// to the PATH the launched child will inherit, unless it's already there.
//
// This matters when ResolveExec located the tool OUTSIDE $PATH (an npm
// global bin dir a version manager never added to PATH): the tool's
// interpreter and siblings live in that SAME dir — a Node CLI like gemini
// is a `#!/usr/bin/env node` script whose `node` sits right next to it. If
// we launch the resolved absolute path without putting that dir on the
// child's PATH, the shebang can't find node and the tool dies instantly
// with a cryptic `env: node: No such file or directory`. Appending the
// dir makes the co-located interpreter (and any npm/sibling the tool
// shells out to) resolvable as a FALLBACK. Deliberately appended, not
// prepended: a node/npm the user already has first on PATH must keep
// winning — a prepend would let a stale off-PATH install (old nvm dir
// exported via NVM_BIN) shadow the working system interpreter.
//
// Idempotent: when execPath is already on $PATH (the LookPath fast path)
// the dir is present and env is returned unchanged; entries are Clean'd
// before comparing so a trailing-separator PATH entry still counts as
// present. The existing PATH key's casing is preserved (Windows exports
// "Path", not "PATH") so mergeEnv overlays the canonical entry instead
// of adding a shadow one. Key matching is exact on POSIX (env names are
// case-sensitive — a stray "Path" is a different variable) and folded
// only on Windows, where the scan keeps the LAST fold-equal entry to
// mirror os/exec's dedupEnv ("in favor of later values") — rewriting an
// earlier duplicate would be silently dropped at Start.
func withExecDirOnPath(env map[string]string, execPath string) map[string]string {
	dir := filepath.Dir(execPath)
	if dir == "" || dir == "." {
		return env
	}
	// Determine the PATH key + value the child would see. An entry in env
	// wins over the ambient one (mergeEnv lets env override os.Environ).
	key, cur := "PATH", ""
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 && pathKeyEquals(kv[:i]) {
			key, cur = kv[:i], kv[i+1:]
			// No break: keep the LAST match (Windows dedupEnv semantics;
			// POSIX names are unique so this loop matches at most once).
		}
	}
	for k, v := range env {
		if pathKeyEquals(k) {
			key, cur = k, v
			break
		}
	}
	for _, p := range filepath.SplitList(cur) {
		if p != "" && samePath(p, dir) {
			return env // already reachable; nothing to do
		}
	}
	out := make(map[string]string, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	if cur == "" {
		out[key] = dir
	} else {
		out[key] = cur + string(os.PathListSeparator) + dir
	}
	return out
}

// pathKeyEquals reports whether an env key names the PATH variable:
// exact on POSIX (env names are case-sensitive), folded on Windows.
func pathKeyEquals(k string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(k, "PATH")
	}
	return k == "PATH"
}

// samePath compares two cleaned paths, case-insensitively on Windows.
// Cleaning matters: hand-edited PATH entries often carry trailing
// separators that LookPath tolerates but a raw == would not.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
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
