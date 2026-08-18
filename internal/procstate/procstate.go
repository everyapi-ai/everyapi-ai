// Package procstate holds the process-liveness probe shared by the parts of the CLI that decide whether to take over state an earlier `everyapi` process left behind — the standalone proxy's PID file and the per-launch prepared client homes.
package procstate

import (
	"os"
	"strings"
	"syscall"
)

// Alive reports whether pid is a running process we can signal.
//
// os.FindProcess on Unix always succeeds — the real liveness check is Signal(0). "Operation not permitted" counts as alive (someone else's PID; not ours, but it IS alive), and so does every other unrecognized error: every caller uses this to decide whether to seize a resource another process may still own, so guessing "alive" is the direction that costs nothing but a delay.
//
// On Windows os.Process.Signal rejects everything except Kill, so the probe degenerates to "did the handle open" — which also succeeds for a process that has exited but not yet been reaped. That errs the same safe way: a dead PID can read as alive here, never the reverse.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		if strings.Contains(err.Error(), "process already finished") ||
			strings.Contains(err.Error(), "no such process") {
			return false
		}
		return true
	}
	return true
}
