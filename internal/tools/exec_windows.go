//go:build windows

package tools

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
)

// Exec on Windows can't execve, so we spawn + wait + propagate exit
// code. The cost is the extra `everyapi` process hangs around as a
// parent until the tool exits; the gain is that the child stays in our
// console process group, so a console Ctrl+C (CTRL_C_EVENT) is
// delivered to the WHOLE group — the child gets it directly and handles
// it itself (Claude Code traps ^C to interrupt, not exit). Mirrors the
// Unix build's Start/Wait structure and shares exitCodeFromWait so both
// platforms classify exits the same way.
//
// We must register an interrupt notifier BEFORE Start, otherwise the Go
// runtime's default disposition kills THIS parent on the shared
// CTRL_C_EVENT before cmd.Wait() returns — orphaning the still-running
// child and losing its real exit code. Registering signal.Notify (and
// then swallowing the signal, since the child already received it via
// the group) overrides that default so the parent survives to reap the
// child and exit with its code. Windows has no SIGQUIT/SIGHUP and the
// console does not generate SIGTERM, so only the interrupt notifier is
// needed (os.Interrupt maps to the console CTRL_C_EVENT; the stdlib
// syscall package exposes no SIGBREAK constant on Windows to add).
//
// extraArgs are passed through as command-line args to the tool, so
// callers can forward user-supplied flags (e.g.
// `--dangerously-skip-permissions` for claude). nil is fine.
func Exec(t *Tool, env map[string]string, extraArgs []string) error {
	return ExecWithOptions(t, ExecOptions{Env: env, Args: extraArgs})
}

// ExecWithOptions is Exec plus explicit inherited-env removal and lifecycle
// cleanup for process-scoped proxies and temporary trust bundles.
func ExecWithOptions(t *Tool, opts ExecOptions) error {
	cleanup := func() {
		if opts.Cleanup != nil {
			opts.Cleanup()
		}
	}
	path, err := ResolveExec(t)
	if err != nil {
		cleanup()
		return &ErrToolNotFound{Tool: t}
	}
	cmd := exec.Command(path, opts.Args...)
	cmd.Args = append([]string{t.ExecName}, opts.Args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergeEnvRemoving(withExecDirOnPath(opts.Env, path), opts.UnsetEnv)

	// Notify is installed BEFORE Start so there's no window where a
	// console Ctrl+C lands on our default (fatal) disposition and
	// orphans a just-started child.
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, os.Interrupt)
	if err := cmd.Start(); err != nil {
		signal.Stop(sigCh)
		cleanup()
		logToolExit(t.ExecName, 0, "start failed: "+err.Error())
		return fmt.Errorf("start %s: %w", t.ExecName, err)
	}
	// Pair the exit line below with a launch line so a hard-killed parent
	// is legible as a "launched, never exited" gap.
	logToolExit(t.ExecName, childPID(cmd), "launched")
	go func() {
		// Drain and swallow: the child already received the event via
		// the shared console group. We register only so the runtime
		// doesn't terminate us before we reap the child.
		for s := range sigCh {
			logToolSignal(t.ExecName, childPID(cmd), s)
		}
	}()

	waitErr := cmd.Wait()
	// Stop delivery, then close so the drain goroutine's range ends and
	// it exits cleanly (signal.Stop alone doesn't close the channel).
	signal.Stop(sigCh)
	close(sigCh)
	cleanup()
	// Record the child's fate before os.Exit skips deferred writes, so a
	// session that dies with no console output still leaves a breadcrumb.
	logToolExit(t.ExecName, childPID(cmd), windowsExitCause(waitErr))
	os.Exit(exitCodeFromWait(waitErr))
	return nil // unreachable
}

// windowsExitCause renders the child's termination for the diagnostic
// log. Windows has no POSIX signal-death status, so a non-zero exit is
// reported by its code (a task killed by the OS surfaces as a non-zero
// code here, not a signal).
func windowsExitCause(waitErr error) string {
	if waitErr == nil {
		return "exit=0 (clean)"
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		return fmt.Sprintf("exit=%d", ee.ExitCode())
	}
	return "wait error: " + waitErr.Error()
}
