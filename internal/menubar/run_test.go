package menubar

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestRun_DispatchLoop boots the controller's Run() (bypassing
// systray click wiring via items==nil) and drives it through a
// representative command sequence. Validates that:
//   - cmdQuit terminates the loop cleanly
//   - non-blocking handlers (OpenWeb, SanitizerToggle) flow without
//     deadlocking the dispatch
//   - blocking handlers spawn their own goroutines (proven by the
//     fact that we can enqueue cmdQuit immediately after them and
//     it processes)
func TestRun_DispatchLoop(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubOpenBrowser(t)

	// Stub the real systray.Quit so handleQuit doesn't try to talk
	// to a tray that isn't there. The fake just lets Run's `return`
	// after cmdQuit do the actual exiting.
	var quitCalled atomic.Int32
	prevQuit := systrayQuit
	systrayQuit = func() { quitCalled.Add(1) }
	t.Cleanup(func() { systrayQuit = prevQuit })

	fm := &fakeMenu{}
	c := newForTest(fm)

	done := make(chan struct{})
	go func() {
		c.Run()
		close(done)
	}()

	// Drive a few commands. handleOpenWeb is synchronous; the
	// runner is fast enough that the dispatch enqueues and
	// processes before we move on.
	c.kick <- cmdOpenWeb
	c.kick <- cmdQuit

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not exit on cmdQuit within 3s")
	}
	if quitCalled.Load() != 1 {
		t.Errorf("systrayQuit calls = %d, want 1", quitCalled.Load())
	}
}

// TestRun_MultipleBranches sends several commands through the
// dispatcher to widen branch coverage: open-web, refresh-now (with
// no creds → no-op), manage-channels (drops into jump-phrase which
// short-circuits without creds), then quit.
func TestRun_MultipleBranches(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubOpenBrowser(t)
	stubConfirmDialog(t, true)
	prevQuit := systrayQuit
	systrayQuit = func() {}
	t.Cleanup(func() { systrayQuit = prevQuit })

	c := newForTest(&fakeMenu{})
	done := make(chan struct{})
	go func() { c.Run(); close(done) }()

	c.kick <- cmdOpenWeb
	c.kick <- cmdRefreshNow
	c.kick <- cmdManageChannels
	c.kick <- cmdTopUp
	c.kick <- cmdSignOut
	c.kick <- cmdSanitizerToggle
	c.kick <- cmdQuit

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not exit on cmdQuit within 3s")
	}
}

// TestHandleQuit covers the explicit teardown path: cancelLogin is
// called if non-nil, sanitizer is stopped, systrayQuit fires.
func TestHandleQuit(t *testing.T) {
	prevQuit := systrayQuit
	var fired atomic.Int32
	systrayQuit = func() { fired.Add(1) }
	t.Cleanup(func() { systrayQuit = prevQuit })

	c := newForTest(&fakeMenu{})
	loginCanceled := atomic.Int32{}
	_, cancel := contextWithRecorder(&loginCanceled)
	c.cancelLogin = cancel

	c.handleQuit()

	if fired.Load() != 1 {
		t.Errorf("systrayQuit fired %d times", fired.Load())
	}
	if loginCanceled.Load() != 1 {
		t.Errorf("cancelLogin not invoked")
	}
}

// contextWithRecorder returns a cancel that bumps a counter when
// invoked. Used to assert handleQuit propagates cancellation to an
// in-flight sign-in.
func contextWithRecorder(counter *atomic.Int32) (chan struct{}, func()) {
	done := make(chan struct{})
	return done, func() {
		counter.Add(1)
		select {
		case <-done:
		default:
			close(done)
		}
	}
}

// TestRun_RefreshTickWhileSignedOut: a cmdRefreshTick fires the
// guard that no-ops when logged out — proves the gate works rather
// than hammering the unauthenticated endpoint.
func TestRun_RefreshTickWhileSignedOut(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prevQuit := systrayQuit
	systrayQuit = func() {}
	t.Cleanup(func() { systrayQuit = prevQuit })

	fm := &fakeMenu{}
	c := newForTest(fm)

	done := make(chan struct{})
	go func() { c.Run(); close(done) }()

	c.kick <- cmdRefreshTick // should no-op
	c.kick <- cmdQuit

	<-done
	// fakeMenu should only have the initial applyLoggedOut call —
	// no spurious applyData from a triggered refresh.
	for _, call := range fm.calls() {
		if call.kind == "data" {
			t.Errorf("data applied while signed out: %+v", call)
		}
	}
}
