package menubar

import "testing"

// TestRefreshFailureStreak walks the streak counter through every
// transition: failures below threshold are silent, the Nth failure
// trips the banner, subsequent failures don't re-fire, and a
// success clears.
func TestRefreshFailureStreak(t *testing.T) {
	fm := &fakeMenu{}
	c := newForTest(fm)

	// Two failures: under the threshold, no menu mutation.
	c.markRefreshFailure()
	c.markRefreshFailure()
	if _, ok := fm.lastOfKind("stale"); ok {
		t.Errorf("stale fired below threshold: %+v", fm.calls())
	}

	// Third failure trips the banner.
	c.markRefreshFailure()
	last, ok := fm.lastOfKind("stale")
	if !ok || last.args[0] != true {
		t.Fatalf("expected stale(true), got %+v", fm.calls())
	}

	// Reset the recorder window so we can detect "did NOT call".
	stalesBefore := countCalls(fm, "stale")
	c.markRefreshFailure()
	if countCalls(fm, "stale")-stalesBefore != 0 {
		t.Error("stale re-fired while already stale (should be idempotent)")
	}

	// Success clears.
	c.markRefreshSuccess()
	last, ok = fm.lastOfKind("stale")
	if !ok || last.args[0] != false {
		t.Errorf("expected stale(false) on recovery, got %+v", fm.calls())
	}

	// Success while not stale must not flap the banner.
	successesBefore := countCalls(fm, "stale")
	c.markRefreshSuccess()
	if countCalls(fm, "stale")-successesBefore != 0 {
		t.Error("stale(false) re-fired while already not-stale")
	}
}

func countCalls(f *fakeMenu, kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.log {
		if c.kind == kind {
			n++
		}
	}
	return n
}
