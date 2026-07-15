//go:build !windows

package tools

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// TestUnixExitCause renders the diagnostic cause string for each fate a
// supervised child can meet. The signal case is the one that matters for
// the "all sessions died silently at once" investigation: a batch of
// children reaped by the same signal (SIGKILL from OOM, SIGHUP from an
// ssh hangup) is only distinguishable after the fact from this text.
func TestUnixExitCause(t *testing.T) {
	if got := unixExitCause(nil); got != "exit=0 (clean)" {
		t.Errorf("clean exit cause = %q", got)
	}

	err := exec.Command("sh", "-c", "exit 7").Run()
	if got := unixExitCause(err); got != "exit=7" {
		t.Errorf("`exit 7` cause = %q, want exit=7", got)
	}

	// SIGKILL(9) is the OOM killer's fingerprint — the exact case the log
	// exists to make legible.
	err = exec.Command("sh", "-c", "kill -KILL $$").Run()
	if got := unixExitCause(err); !strings.Contains(got, "signal 9") {
		t.Errorf("SIGKILL cause = %q, want it to name signal 9", got)
	}

	// SIGHUP(1) is the ssh-disconnect fingerprint.
	err = exec.Command("sh", "-c", "kill -HUP $$").Run()
	if got := unixExitCause(err); !strings.Contains(got, "signal 1") {
		t.Errorf("SIGHUP cause = %q, want it to name signal 1", got)
	}
}

// TestSignalCauseNamesNumber verifies the parent-received-signal line
// carries the signal NUMBER, matching the "signal N" shape of the child
// exit line so an operator can grep one number across both.
func TestSignalCauseNamesNumber(t *testing.T) {
	if got := signalCause(syscall.SIGTERM); !strings.Contains(got, "signal 15") {
		t.Errorf("SIGTERM signalCause = %q, want it to contain 'signal 15'", got)
	}
	if got := signalCause(syscall.SIGHUP); !strings.Contains(got, "signal 1") {
		t.Errorf("SIGHUP signalCause = %q, want it to contain 'signal 1'", got)
	}
}
