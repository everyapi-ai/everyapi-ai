//go:build !windows

package tools

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// Exec launches the target tool as a CHILD process and stays alive as
// its parent until it exits, then exits with the child's status code.
//
// It used to `execve` (replacing this process with the tool), which is
// simpler but incompatible with running the sanitizer proxy in-process:
// an in-process goroutine can't outlive an execve. By supervising the
// tool as a child instead, the parent (and any in-process resource it
// owns, like the sanitizer) lives for exactly the tool's lifetime. The
// Windows build already worked this way (no execve there).
//
// Signal handling depends on whether we're the controlling terminal's
// foreground process group (the normal `everyapi use` case launched from
// an interactive shell) — see the inline comment at the Notify site for
// the full disposition table.
//
// Returns only on a failure to start; on success it does not return
// (os.Exit with the child's code). extraArgs are appended to argv after
// t.ExecName so callers can forward user flags (e.g.
// `--dangerously-skip-permissions` for claude). nil is fine.
func Exec(t *Tool, env map[string]string, extraArgs []string) error {
	path, err := exec.LookPath(t.ExecName)
	if err != nil {
		return &ErrToolNotFound{Tool: t}
	}
	cmd := exec.Command(path, extraArgs...)
	cmd.Args = append([]string{t.ExecName}, extraArgs...)
	cmd.Env = mergeEnv(env)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// We set no SysProcAttr.Setpgid, so the child stays in our process
	// group. When that group is the controlling terminal's FOREGROUND
	// group (interactive launch from a shell), the kernel delivers
	// terminal-generated signals to the child directly:
	//   - SIGINT (^C) / SIGQUIT (^\): the child gets them via the group
	//     and handles them itself (Claude Code traps ^C to interrupt, not
	//     exit). We Notify only so OUR default disposition doesn't kill us
	//     before we reap the child, then swallow them.
	//   - SIGHUP (terminal/session hangup): also delivered to the group,
	//     so the child already gets it — we swallow rather than forward, to
	//     avoid double-delivery, but stay alive to reap the child.
	//   - SIGTSTP/SIGCONT (job control) and SIGWINCH (resize) are left at
	//     default disposition so suspend/resume/redraw ride the group
	//     without our interference.
	//   - SIGTERM: never a terminal signal; it arrives only when someone
	//     `kill`s us specifically, so we forward it to the child (then
	//     wait — we don't self-exit and orphan a child mid-shutdown).
	//
	// When we are NOT the controlling terminal's foreground group (no
	// controlling TTY at all: launched by `mcp install`, a wrapper, a
	// pipe), the kernel does NOT deliver SIGINT/SIGQUIT to the child via
	// the group, so we forward those too — otherwise swallowing them would
	// leave the child uninterruptible. We use the real foreground-group
	// check (TIOCGPGRP), not "is stdin a TTY": the latter is wrong for
	// `everyapi use claude < file` (stdin redirected but the process is
	// still foreground, so the kernel already delivers ^C → forwarding
	// would double it).
	//
	// Notify is installed BEFORE Start so there's no window where a ^C
	// lands on our default (fatal) disposition and orphans a just-started
	// child.
	forwardInterrupts := !inForegroundGroup()
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	if err := cmd.Start(); err != nil {
		signal.Stop(sigCh)
		return fmt.Errorf("start %s: %w", t.ExecName, err)
	}
	go func() {
		for s := range sigCh {
			switch s {
			case syscall.SIGTERM:
				_ = cmd.Process.Signal(s)
			case syscall.SIGINT, syscall.SIGQUIT:
				if forwardInterrupts {
					_ = cmd.Process.Signal(s)
				}
				// else: the kernel already delivered it to the child via
				// the shared foreground group; forwarding would double it.
			}
			// SIGHUP: always swallowed (the group already got it on hangup).
		}
	}()

	waitErr := cmd.Wait()
	// Stop delivery, then close so the forwarder goroutine's range ends
	// and it exits cleanly (signal.Stop alone doesn't close the channel).
	signal.Stop(sigCh)
	close(sigCh)
	os.Exit(unixExitCode(waitErr))
	return nil // unreachable
}

// unixExitCode is exitCodeFromWait plus Unix signal-death mapping: a
// child killed by a signal becomes 128+signo (e.g. 130 for SIGINT),
// matching shell convention. Everything else defers to the shared
// exitCodeFromWait so both platforms classify normal exits identically.
func unixExitCode(waitErr error) int {
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}
	return exitCodeFromWait(waitErr)
}

// inForegroundGroup reports whether our process group is the foreground
// process group of our CONTROLLING terminal — i.e. the kernel delivers
// terminal-generated signals (SIGINT/SIGQUIT) to our child directly, so
// the parent must NOT also forward them (that would double-deliver).
//
// It asks /dev/tty (the controlling terminal) directly rather than
// probing stdin/stdout/stderr. On macOS, TIOCGPGRP on a pipe fd succeeds
// with foreground pgrp 0 (not ENOTTY as on Linux), so a per-std-fd probe
// is fooled by a redirected/piped stream (`cmd | everyapi use claude`)
// into the wrong answer. /dev/tty anchors on the real ctty regardless of
// what stdin/out/err are; it fails to open when there's no controlling
// terminal, which is the correct "not foreground → forward" signal for
// non-interactive launches.
func inForegroundGroup() bool {
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return false // no controlling terminal
	}
	defer tty.Close()
	fg, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return false
	}
	return fg == syscall.Getpgrp()
}
