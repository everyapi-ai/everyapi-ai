package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// preparedHomeTestRoot redirects ConfigDir() at a fresh tmp dir for one test by hijacking XDG_CONFIG_HOME (which the SDK's ConfigDir honors first) and returns the sessions root the sweep operates on.
func preparedHomeTestRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root, err := preparedHomeRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

// seedPreparedHome plants a home under root as an earlier launch would have left it. owner < 0 means no owner file at all (a pre-sweep CLI build, or a launch killed between MkdirTemp and the owner write).
func seedPreparedHome(t *testing.T, root, name string, owner int, age time.Duration) string {
	t.Helper()
	home := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if owner >= 0 {
		if err := os.WriteFile(filepath.Join(home, preparedHomeOwnerFile), []byte(strconv.Itoa(owner)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Backdate last, so the writes above cannot bump the directory's ModTime past the age the test asked for.
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(home, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return home
}

// stubOwnerLiveness pins the liveness answer for the duration of one test, so both branches of the policy are reachable without manufacturing real live and dead PIDs. procstate.Alive itself is covered by its own package's tests.
func stubOwnerLiveness(t *testing.T, alive bool) {
	t.Helper()
	previous := preparedHomeOwnerAlive
	preparedHomeOwnerAlive = func(int) bool { return alive }
	t.Cleanup(func() { preparedHomeOwnerAlive = previous })
}

func TestNewPreparedHomeStampsItsOwner(t *testing.T) {
	preparedHomeTestRoot(t)
	home, err := newPreparedHome("codex")
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := readPreparedHomeOwner(home)
	if !ok {
		t.Fatal("new prepared home carries no owner file; a hard kill would leave it indistinguishable from a live session")
	}
	if owner != os.Getpid() {
		t.Fatalf("owner = %d, want this process %d", owner, os.Getpid())
	}
}

// TestCondemnStalePreparedHomesReapsDeadOwner covers the case the sweep exists for: the parent was SIGKILLed, so TakePreparedCleanup never ran and the home outlived the launch.
func TestCondemnStalePreparedHomesReapsDeadOwner(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	home := seedPreparedHome(t, root, "codex-dead", 424242, time.Minute)

	condemned := condemnStalePreparedHomes(root, time.Now())

	if len(condemned) != 1 {
		t.Fatalf("condemned = %v, want the one orphaned home", condemned)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("orphaned home still present under its live name: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(condemned[0]), preparedHomeReapPrefix) {
		t.Fatalf("condemned path %q is not renamed out of the live namespace", condemned[0])
	}
}

// TestCondemnStalePreparedHomesKeepsLiveOwner is the invariant that matters most: an `everyapi use` session running for days must never have its home deleted out from under it by a second launch.
func TestCondemnStalePreparedHomesKeepsLiveOwner(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, true)
	home := seedPreparedHome(t, root, "codex-live", 424242, 10*24*time.Hour)

	if condemned := condemnStalePreparedHomes(root, time.Now()); len(condemned) != 0 {
		t.Fatalf("condemned = %v, want nothing while the owner is alive", condemned)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("live session's home was reaped: %v", err)
	}
}

// TestCondemnStalePreparedHomesReapsPastAbandonedAge covers PID reuse after a reboot, where the recorded PID reads as alive but belongs to an unrelated process.
func TestCondemnStalePreparedHomesReapsPastAbandonedAge(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, true)
	seedPreparedHome(t, root, "codex-reused-pid", 424242, abandonedPreparedHomeAge+time.Hour)

	if condemned := condemnStalePreparedHomes(root, time.Now()); len(condemned) != 1 {
		t.Fatalf("condemned = %v, want the home whose live-looking PID is reuse", condemned)
	}
}

func TestCondemnStalePreparedHomesAgesOutUnownedHomes(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	fresh := seedPreparedHome(t, root, "codex-unowned-fresh", -1, time.Hour)
	seedPreparedHome(t, root, "codex-unowned-old", -1, unownedPreparedHomeAge+time.Hour)

	condemned := condemnStalePreparedHomes(root, time.Now())

	if len(condemned) != 1 || !strings.Contains(condemned[0], "codex-unowned-old") {
		t.Fatalf("condemned = %v, want only the aged-out unowned home", condemned)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("recent unowned home was reaped; a launch killed before its owner write would take a live session with it: %v", err)
	}
}

// TestCondemnStalePreparedHomesHonorsKeepMarker guards the one directory the CLI leaves behind on purpose — the home whose Codex session index could not be merged back, which the user is told to go salvage.
func TestCondemnStalePreparedHomesHonorsKeepMarker(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	home := seedPreparedHome(t, root, "codex-kept", 424242, abandonedPreparedHomeAge+time.Hour)
	keepPreparedHome(home)

	if condemned := condemnStalePreparedHomes(root, time.Now()); len(condemned) != 0 {
		t.Fatalf("condemned = %v, want the deliberately kept home left alone", condemned)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("deliberately kept home was reaped: %v", err)
	}
}

// TestCondemnStalePreparedHomesFinishesAnInterruptedReap covers a sweep whose background delete was cut short by an early exit: the tombstone is out of the live namespace already, so it is finished unconditionally.
func TestCondemnStalePreparedHomesFinishesAnInterruptedReap(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, true)
	tombstone := filepath.Join(root, preparedHomeReapPrefix+"codex-halfdeleted")
	if err := os.MkdirAll(filepath.Join(tombstone, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}

	condemned := condemnStalePreparedHomes(root, time.Now())

	if len(condemned) != 1 || condemned[0] != tombstone {
		t.Fatalf("condemned = %v, want the leftover tombstone %q", condemned, tombstone)
	}
}

// TestSweepStalePreparedHomesDeletesCondemnedHomes exercises the detached delete the launch path actually calls.
func TestSweepStalePreparedHomesDeletesCondemnedHomes(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	seedPreparedHome(t, root, "codex-dead", 424242, time.Minute)

	sweepStalePreparedHomes(root)

	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sessions root still holds %d entries after the sweep", len(entries))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSweepStalePreparedHomesLeavesSymlinkTargetsAlone is the guarantee that makes reaping safe at all: Codex points its rollouts and archive at the persistent codex-home through symlinks, so deleting a home must delete the link and not the durable conversation record behind it.
func TestSweepStalePreparedHomesLeavesSymlinkTargetsAlone(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	persistent := filepath.Join(filepath.Dir(root), "codex-home", "sessions")
	if err := os.MkdirAll(persistent, 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(persistent, "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte("{\"id\":\"kept\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := seedPreparedHome(t, root, "codex-linked", 424242, time.Minute)
	link := filepath.Join(home, "linked-sessions")
	if err := os.Symlink(persistent, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	condemned := condemnStalePreparedHomes(root, time.Now())
	if len(condemned) != 1 {
		t.Fatalf("condemned = %v, want the orphaned home", condemned)
	}
	if err := os.RemoveAll(condemned[0]); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(rollout); err != nil {
		t.Fatalf("reaping a session home destroyed durable state behind its symlink: %v", err)
	}
}

// TestNewPreparedHomeReapsOrphansBeforeItsOwn confirms the sweep runs on the launch path and cannot mistake the home it is about to create for an orphan.
func TestNewPreparedHomeReapsOrphansBeforeItsOwn(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	orphan := seedPreparedHome(t, root, "codex-dead", 424242, time.Minute)

	home, err := newPreparedHome("codex")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("launch did not reclaim the orphaned home: %v", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("launch reaped the home it just created: %v", err)
	}
}

// TestTakePreparedCleanupKeepsTheHomeItCannotMerge pairs the warning printed on a failed session-index merge with a marker, so the next launch does not delete the directory the user was just told to go look at.
func TestTakePreparedCleanupKeepsTheHomeItCannotMerge(t *testing.T) {
	root := preparedHomeTestRoot(t)
	home, err := newPreparedHome("codex")
	if err != nil {
		t.Fatal(err)
	}
	// No .session_index.baseline.jsonl in the home, so the merge fails on its first read.
	env := preparedHomeEnv("CODEX_HOME", home)
	env[preparedCodexSessionIndexMarker] = filepath.Join(root, "session_index.jsonl")

	cleanup := TakePreparedCleanup(env)
	if cleanup == nil {
		t.Fatal("TakePreparedCleanup returned no cleanup for a prepared home")
	}
	cleanup()

	if _, err := os.Stat(home); err != nil {
		t.Fatalf("home was removed despite the failed merge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, preparedHomeKeepFile)); err != nil {
		t.Fatalf("kept home carries no keep marker, so the next sweep would reap it: %v", err)
	}
	stubOwnerLiveness(t, false)
	if condemned := condemnStalePreparedHomes(root, time.Now().Add(abandonedPreparedHomeAge)); len(condemned) != 0 {
		t.Fatalf("condemned = %v, want the kept home to survive later sweeps", condemned)
	}
}
