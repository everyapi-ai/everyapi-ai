//go:build windows

package tools

import (
	"os/exec"
	"strings"
	"testing"
)

// TestWindowsExitCause covers the Windows cause-rendering path, which the
// unix-tagged diaglog test can't compile. Windows has no POSIX signal
// death, so every termination surfaces as an exit code (a task killed by
// the OS included).
func TestWindowsExitCause(t *testing.T) {
	if got := windowsExitCause(nil); got != "exit=0 (clean)" {
		t.Errorf("clean exit cause = %q", got)
	}

	// A real non-zero exit from a child process.
	err := exec.Command("cmd", "/c", "exit 7").Run()
	if got := windowsExitCause(err); got != "exit=7" {
		t.Errorf("`exit 7` cause = %q, want exit=7", got)
	}

	// A non-ExitError wait failure falls through to the wait-error branch.
	if got := windowsExitCause(exec.ErrNotFound); !strings.HasPrefix(got, "wait error:") {
		t.Errorf("non-exit error cause = %q, want 'wait error:' prefix", got)
	}
}
