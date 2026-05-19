package menubar

import (
	"net"
	"testing"
	"time"
)

// TestSanitizerRunner_StartStopRunning exercises the runner against
// the real sanitizer.Server bound on an ephemeral loopback port —
// integration in spirit but cheap (no upstream traffic, no DNS).
func TestSanitizerRunner_StartStopRunning(t *testing.T) {
	r := &sanitizerRunner{}

	if r.Running() {
		t.Fatal("fresh runner reports Running()=true")
	}
	if r.Listen() != "" {
		t.Errorf("fresh runner Listen() = %q, want empty", r.Listen())
	}

	// Pick a free port up-front so the bind is guaranteed to
	// succeed; the runner re-binds the same address moments later.
	addr := freeLoopbackPort(t)

	if err := r.Start(addr, "https://example.invalid"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !r.Running() {
		t.Error("after Start: Running()=false")
	}
	if got := r.Listen(); got != addr {
		t.Errorf("Listen() = %q, want %q", got, addr)
	}
	// Second Start while running is a no-op.
	if err := r.Start(addr, "https://example.invalid"); err != nil {
		t.Errorf("idempotent Start returned err: %v", err)
	}

	r.Stop()
	// Stop returns immediately; the underlying Run goroutine
	// clears state on its own. Poll briefly.
	if !waitFor(func() bool { return !r.Running() }, 500*time.Millisecond) {
		t.Errorf("after Stop: Running() still true")
	}
	if r.Listen() != "" {
		t.Errorf("after Stop: Listen() = %q, want empty", r.Listen())
	}
	// Stop while stopped is a no-op.
	r.Stop()
}

// freeLoopbackPort asks the kernel for an unused port, closes the
// listener immediately, and returns the address string for the
// caller to bind. There's a tiny TOCTOU window before re-bind but
// it's fine for a unit test.
func freeLoopbackPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ephemeral listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitFor polls cond every 10ms up to timeout. Returns true once
// cond returns true, false on timeout. Used by tests where the
// background sanitizer goroutine flips state asynchronously.
func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
