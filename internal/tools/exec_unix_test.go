//go:build !windows

package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestUnixExitCode covers the three classifications: clean exit, a
// normal non-zero exit code, and termination by signal (128+signo).
// unixExitCode is what the Unix Exec actually propagates; it layers the
// signal mapping over the shared exitCodeFromWait.
func TestUnixExitCode(t *testing.T) {
	if got := unixExitCode(nil); got != 0 {
		t.Errorf("nil wait err -> %d, want 0", got)
	}

	// Normal non-zero exit.
	err := exec.Command("sh", "-c", "exit 7").Run()
	if got := unixExitCode(err); got != 7 {
		t.Errorf("`exit 7` -> %d, want 7", got)
	}

	// Killed by a signal: the shell SIGTERMs itself -> 128+15 = 143.
	err = exec.Command("sh", "-c", "kill -TERM $$").Run()
	if got := unixExitCode(err); got != 143 {
		t.Errorf("self-SIGTERM -> %d, want 143 (128+SIGTERM)", got)
	}

	// The shared base mapping (no signal layer): signaled -> ExitCode()
	// which is -1, normal exit -> its code, nil -> 0.
	if got := exitCodeFromWait(nil); got != 0 {
		t.Errorf("exitCodeFromWait(nil) -> %d, want 0", got)
	}
	if got := exitCodeFromWait(exec.Command("sh", "-c", "exit 7").Run()); got != 7 {
		t.Errorf("exitCodeFromWait(`exit 7`) -> %d, want 7", got)
	}
}

// TestExecPropagatesChildExitCode runs the test binary as a subprocess
// that calls Exec with a fake "tool" (`sh -c 'exit 7'`). Exec launches
// it as a child, waits, and os.Exits with its code — so the subprocess
// must exit 7. This is the standard os/exec helper-process pattern; it
// exercises the real Start/Wait/exit-code path without a TTY and without
// killing the outer test process (Exec's os.Exit only fires in the
// child). The helper branch is gated by an env var so it's inert under
// normal `go test`.
func TestExecPropagatesChildExitCode(t *testing.T) {
	if os.Getenv("EVERYAPI_EXEC_HELPER") == "1" {
		// Child mode: Exec should replace our exit code with the tool's.
		_ = Exec(&Tool{Name: "sh", ExecName: "sh"}, nil, []string{"-c", "exit 7"})
		// Exec os.Exits on success; reaching here means it returned.
		os.Exit(99)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestExecPropagatesChildExitCode$", "-test.v=false")
	// Redirect the helper's config dir into a temp dir: Exec now writes a
	// use.log launch/exit line, and without this the helper would append
	// to the developer's real ~/.config/everyapi/use.log.
	cmd.Env = append(os.Environ(),
		"EVERYAPI_EXEC_HELPER=1",
		"XDG_CONFIG_HOME="+t.TempDir(),
	)
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("helper did not exit non-zero: %v", err)
	}
	if ee.ExitCode() != 7 {
		t.Fatalf("helper exit code = %d, want 7 (child's code, not 99 'Exec returned')", ee.ExitCode())
	}
}

func TestExecWithOptionsRunsCleanupBeforeExit(t *testing.T) {
	if os.Getenv("EVERYAPI_EXEC_CLEANUP_HELPER") == "1" {
		marker := os.Getenv("EVERYAPI_EXEC_CLEANUP_MARKER")
		_ = ExecWithOptions(&Tool{Name: "sh", ExecName: "sh"}, ExecOptions{
			Args: []string{"-c", "exit 0"},
			Cleanup: func() {
				_ = os.WriteFile(marker, []byte("clean"), 0o600)
			},
		})
		os.Exit(99)
		return
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "cleanup-ran")
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecWithOptionsRunsCleanupBeforeExit$", "-test.v=false")
	// Redirect the helper's config dir so its use.log write lands in the
	// temp dir, not the developer's real ~/.config/everyapi.
	cmd.Env = append(os.Environ(),
		"EVERYAPI_EXEC_CLEANUP_HELPER=1",
		"EVERYAPI_EXEC_CLEANUP_MARKER="+marker,
		"XDG_CONFIG_HOME="+dir,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "clean" {
		t.Fatalf("cleanup marker = %q, err=%v", body, err)
	}
}
