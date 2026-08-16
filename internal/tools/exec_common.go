package tools

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

// useExitLogMaxBytes caps ~/.config/everyapi/use.log so a long-lived install can't grow it without bound. When a write would cross this size the oldest lines are dropped and only the most recent useExitLogRetainBytes are kept — a real rolling tail, not a wipe, so the launch/exit history needed to correlate a mass-death survives.
const useExitLogMaxBytes = 1 << 20

// useExitLogRetainBytes is how much of the tail to keep when rolling. Half the cap leaves plenty of recent history while guaranteeing the file shrinks on every roll.
const useExitLogRetainBytes = useExitLogMaxBytes / 2

// useLogWriteTimeout bounds how long the exit/signal path will wait for the diagnostic write before giving up. The write runs on its own goroutine; if the config dir lives on a stalled network mount (an NFS/autofs home is exactly the shared-host environment this feature targets), the parent abandons the write instead of blocking os.Exit — the diagnostic must never become the hang it exists to explain.
const useLogWriteTimeout = 500 * time.Millisecond

// logToolExit records why a supervised tool child terminated, so a launch that vanishes with no output on the terminal still leaves a durable breadcrumb. It is the diagnostic answer to "all my sessions died at once with no message": each `everyapi use` process writes one line naming the tool, its pid, and the precise termination cause (clean exit code, or death by signal N) with a timestamp. Correlating those lines across sessions reveals whether a batch of children was reaped by the same signal at the same instant — the fingerprint of an external reaper (OOM killer → SIGKILL, an ssh/session hangup → SIGHUP) rather than a per-process bug.
//
// The write is dispatched to a goroutine and awaited only up to useLogWriteTimeout, so a wedged filesystem can never stall the caller (notably the exit path, which runs this immediately before os.Exit). Best-effort throughout: any failure — timeout, unresolved config dir, I/O error — is swallowed. Diagnostics must not change the exit path or keep the tool from launching.
func logToolExit(execName string, pid int, cause string) {
	line := fmt.Sprintf("%s pid=%d tool=%s %s\n",
		time.Now().Format("2006-01-02T15:04:05.000Z07:00"), pid, execName, cause)
	done := make(chan struct{})
	go func() {
		defer close(done)
		writeUseLogLine(line)
	}()
	select {
	case <-done:
	case <-time.After(useLogWriteTimeout):
		// Abandon the (possibly blocked) write goroutine. It holds no lock the caller needs and will exit if/when the FS responds; leaking it for the brief remainder of process life is fine.
	}
}

// signalCause renders a received signal identically to the child-exit line's "signal N (name)" shape, so an operator can grep a single "signal 15" across both the parent-received line and the paired child exit line. os.Signal.String() alone yields the name only ("terminated"), which would not match the number-keyed exit line.
func signalCause(sig os.Signal) string {
	if s, ok := sig.(syscall.Signal); ok {
		return fmt.Sprintf("parent received signal %d (%s)", int(s), s)
	}
	return "parent received signal " + sig.String()
}

// logToolSignal records that the supervising parent itself received a terminal/session signal, using the same timeout-guarded path as logToolExit.
func logToolSignal(execName string, pid int, sig os.Signal) {
	logToolExit(execName, pid, signalCause(sig))
}

// writeUseLogLine performs the actual, synchronous, blocking write of one pre-formatted line to ~/.config/everyapi/use.log. It is only ever run from logToolExit's timeout-guarded goroutine, never inline. Concurrent writers (independent `everyapi use` processes and the signal goroutine) are serialized with an advisory file lock so their lines can't interleave or clobber each other, and the file is tail-trimmed under that same lock when it would exceed the cap.
func writeUseLogLine(line string) {
	path, err := config.EnsureLogPath("use.log")
	if err != nil {
		return
	}
	// O_RDWR, NOT O_APPEND: we serialize writers with an advisory lock and position writes explicitly, and O_APPEND would both defeat the in-place tail trim (Go rejects WriteAt on an append fd) and force every write to end-of-file regardless of our seeks.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	// Hold an advisory exclusive lock across the size check, the tail trim, and the append so no other writer can rewrite under us or interleave its line. lockUseLog is a no-op fallback where the OS lacks advisory locks; that path is best-effort only.
	unlock := lockUseLog(f)
	defer unlock()

	if info, statErr := f.Stat(); statErr == nil && info.Size()+int64(len(line)) > useExitLogMaxBytes {
		// Trim IN PLACE on this same fd so the lock we hold stays valid (a rename would swap the inode out from under the lock) and leave the offset at the new end. If the trim can't complete, fall through and append anyway — an oversized log beats a lost death line.
		trimUseLogTailInPlace(f)
	} else if _, err := f.Seek(0, os.SEEK_END); err != nil {
		return
	}
	_, _ = f.WriteString(line)
}

// trimUseLogTailInPlace rewrites f to keep only its last useExitLogRetainBytes, aligned to a line boundary, reusing the same open descriptor (and thus the caller's advisory lock and the same inode). It leaves the offset at the new end so the caller's append continues cleanly. Best-effort: on any error f is left unchanged.
func trimUseLogTailInPlace(f *os.File) {
	if _, err := f.Seek(0, os.SEEK_SET); err != nil {
		return
	}
	data, err := readAllFromFile(f)
	if err != nil {
		return
	}
	if len(data) <= useExitLogRetainBytes {
		if _, err := f.Seek(0, os.SEEK_END); err != nil {
			return
		}
		return
	}
	tail := data[len(data)-useExitLogRetainBytes:]
	// Drop a leading partial line so the retained tail starts clean.
	if nl := bytes.IndexByte(tail, '\n'); nl >= 0 && nl+1 <= len(tail) {
		tail = tail[nl+1:]
	}
	// Overwrite from the start, then truncate to the new length. Order matters: write first (so the bytes are in place) then shrink.
	if _, err := f.WriteAt(tail, 0); err != nil {
		_, _ = f.Seek(0, os.SEEK_END)
		return
	}
	if err := f.Truncate(int64(len(tail))); err != nil {
		_, _ = f.Seek(0, os.SEEK_END)
		return
	}
	_, _ = f.Seek(int64(len(tail)), os.SEEK_SET)
}

// readAllFromFile reads f to EOF from its current offset without pulling in io/ioutil; kept local so exec_common.go's import set stays minimal.
func readAllFromFile(f *os.File) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// childPID returns the started child's pid, or 0 if the process handle is absent (never started / already released). Shared by both platform launchers — it touches only the platform-neutral *exec.Cmd.
func childPID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

// ErrToolNotFound is returned by Exec when the tool's executable isn't on $PATH. cmd/use renders the InstallHint via Error().
type ErrToolNotFound struct {
	Tool *Tool
}

// ExecOptions describes one supervised tool launch. Cleanup runs after the child exits and before the parent propagates its exit code, and also runs if launch setup fails. UnsetEnv removes inherited values instead of replacing them with observably-present empty strings.
type ExecOptions struct {
	Env      map[string]string
	UnsetEnv []string
	Args     []string
	Cleanup  func()
}

func (e *ErrToolNotFound) Error() string {
	return fmt.Sprintf("%s is not installed.\n  %s", e.Tool.ExecName, e.Tool.InstallHint)
}

// mergeEnv overlays `set` onto os.Environ(): each key in `set` replaces the existing var with the same name; keys not in `set` pass through unchanged. Returns a fresh []string in KEY=VAL form suitable for exec.Command's Env.
//
// Why build an explicit slice rather than os.Setenv + reuse os.Environ(): the child process only sees the env we hand exec.Command, so passing the merged slice is the correct, side-effect-free contract on both Unix and Windows.
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

// withExecDirOnPath returns env with the directory of execPath APPENDED to the PATH the launched child will inherit, unless it's already there.
//
// This matters when ResolveExec located the tool OUTSIDE $PATH (an npm global bin dir a version manager never added to PATH): the tool's interpreter and siblings live in that SAME dir — a Node CLI like gemini is a `#!/usr/bin/env node` script whose `node` sits right next to it. If we launch the resolved absolute path without putting that dir on the child's PATH, the shebang can't find node and the tool dies instantly with a cryptic `env: node: No such file or directory`. Appending the dir makes the co-located interpreter (and any npm/sibling the tool shells out to) resolvable as a FALLBACK. Deliberately appended, not prepended: a node/npm the user already has first on PATH must keep winning — a prepend would let a stale off-PATH install (old nvm dir exported via NVM_BIN) shadow the working system interpreter.
//
// Idempotent: when execPath is already on $PATH (the LookPath fast path) the dir is present and env is returned unchanged; entries are Clean'd before comparing so a trailing-separator PATH entry still counts as present. The existing PATH key's casing is preserved (Windows exports "Path", not "PATH") so mergeEnv overlays the canonical entry instead of adding a shadow one. Key matching is exact on POSIX (env names are case-sensitive — a stray "Path" is a different variable) and folded only on Windows, where the scan keeps the LAST fold-equal entry to mirror os/exec's dedupEnv ("in favor of later values") — rewriting an earlier duplicate would be silently dropped at Start.
func withExecDirOnPath(env map[string]string, execPath string) map[string]string {
	dir := filepath.Dir(execPath)
	if dir == "" || dir == "." {
		return env
	}
	// Determine the PATH key + value the child would see. An entry in env wins over the ambient one (mergeEnv lets env override os.Environ).
	key, cur := "PATH", ""
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 && pathKeyEquals(kv[:i]) {
			key, cur = kv[:i], kv[i+1:]
			// No break: keep the LAST match (Windows dedupEnv semantics; POSIX names are unique so this loop matches at most once).
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

// pathKeyEquals reports whether an env key names the PATH variable: exact on POSIX (env names are case-sensitive), folded on Windows.
func pathKeyEquals(k string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(k, "PATH")
	}
	return k == "PATH"
}

// samePath compares two cleaned paths, case-insensitively on Windows. Cleaning matters: hand-edited PATH entries often carry trailing separators that LookPath tolerates but a raw == would not.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// exitCodeFromWait maps a cmd.Wait() error to the process exit code we should propagate: 0 on clean exit, the child's own code on a normal non-zero exit, and 1 for anything we can't classify. Shared by the Unix and Windows launchers so both platforms agree on the common cases; the Unix launcher layers signal-death mapping (128+signo) on top before falling through to this (see exec_unix.go).
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
