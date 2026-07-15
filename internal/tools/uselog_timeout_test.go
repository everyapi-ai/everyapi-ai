//go:build !windows

package tools

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestLogToolExitTimesOutOnBlockingWrite guards the exit-path-can't-hang
// property: logToolExit must return within ~useLogWriteTimeout even when
// the underlying write blocks forever. This is the whole reason the write
// runs on a timeout-guarded goroutine — on a stalled NFS/autofs home (the
// shared-host environment this feature targets) the parent must abandon
// the diagnostic, not wedge os.Exit and reproduce the very "session
// vanished" hang it exists to explain.
//
// A FIFO with no reader makes OpenFile(O_RDWR) on use.log block
// indefinitely, standing in for the stalled mount.
func TestLogToolExitTimesOutOnBlockingWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg := filepath.Join(dir, "everyapi")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(cfg, "use.log")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}

	start := time.Now()
	logToolExit("claude", 1, "exit=0 (clean)")
	elapsed := time.Since(start)

	if elapsed > useLogWriteTimeout+2*time.Second {
		t.Fatalf("logToolExit blocked %v, want it to give up near %v — the exit path must never hang",
			elapsed, useLogWriteTimeout)
	}

	// Unblock the leaked writer goroutine so it can exit: opening a
	// non-blocking reader lets its pending O_RDWR open complete.
	if r, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
		_ = r.Close()
	}
}
