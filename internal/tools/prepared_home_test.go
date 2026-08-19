package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Recorded owners for the seeded homes below. Real PIDs are never signalled in these tests — preparedHomeOwnerAlive is stubbed — so the numbers only have to be distinguishable from each other.
const (
	deadParent = 424242
	deadChild  = 424243
	liveChild  = 424244
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

// seedPreparedHome plants a home under root as an earlier launch would have left it. No owners means no owner file at all (a pre-sweep CLI build, or a launch killed between MkdirTemp and the owner write); the usual case is two, the launching process and the tool it started.
func seedPreparedHome(t *testing.T, root, name string, age time.Duration, owners ...int) string {
	t.Helper()
	home := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(owners) > 0 {
		recorded := ""
		for _, owner := range owners {
			recorded += strconv.Itoa(owner) + "\n"
		}
		if err := os.WriteFile(filepath.Join(home, preparedHomeOwnerFile), []byte(recorded), 0o600); err != nil {
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
	stubOwnerLivenessOf(t, func(int) bool { return alive })
}

// stubOwnerLivenessOf pins the answer per PID, for the cases where a home's owners disagree.
func stubOwnerLivenessOf(t *testing.T, alive func(int) bool) {
	t.Helper()
	previous := preparedHomeOwnerAlive
	preparedHomeOwnerAlive = alive
	t.Cleanup(func() { preparedHomeOwnerAlive = previous })
	launchPreparedHome = ""
	t.Cleanup(func() { launchPreparedHome = "" })
}

func TestNewPreparedHomeStampsItsOwner(t *testing.T) {
	preparedHomeTestRoot(t)
	home, err := newPreparedHome("codex")
	if err != nil {
		t.Fatal(err)
	}
	owners := readPreparedHomeOwners(home)
	if len(owners) != 1 || owners[0] != os.Getpid() {
		t.Fatalf("owners = %v, want just this process %d; without one, a hard kill leaves a home indistinguishable from a live session", owners, os.Getpid())
	}
}

// TestCondemnStalePreparedHomesReapsDeadOwner covers the case the sweep exists for: the parent was SIGKILLed, so TakePreparedCleanup never ran and the home outlived the launch.
func TestCondemnStalePreparedHomesReapsDeadOwner(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	home := seedPreparedHome(t, root, "codex-dead", time.Minute, deadParent, deadChild)

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

// TestCondemnStalePreparedHomesKeepsAHomeWhoseToolStillRuns is the invariant that matters most, in the shape that is easiest to get wrong: the launching process was hard-killed, but the tool it started survived it — reparented to init, in its own session — and is still working out of this home. Recording only the parent would hand a live session's directory to the next launch to delete.
func TestCondemnStalePreparedHomesKeepsAHomeWhoseToolStillRuns(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLivenessOf(t, func(pid int) bool { return pid == liveChild })
	home := seedPreparedHome(t, root, "codex-live", 10*24*time.Hour, deadParent, liveChild)

	if condemned := condemnStalePreparedHomes(root, time.Now()); len(condemned) != 0 {
		t.Fatalf("condemned = %v, want nothing while the launched tool is alive", condemned)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("live session's home was reaped: %v", err)
	}
}

// TestAdoptPreparedHomeRecordsTheLaunchedTool covers the handoff that puts the child on the owner file in the first place.
func TestAdoptPreparedHomeRecordsTheLaunchedTool(t *testing.T) {
	preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	home, err := newPreparedHome("codex")
	if err != nil {
		t.Fatal(err)
	}
	env := preparedHomeEnv("CODEX_HOME", home)
	if cleanup := TakePreparedCleanup(env); cleanup == nil {
		t.Fatal("TakePreparedCleanup returned no cleanup for a prepared home")
	}

	adoptPreparedHome(liveChild)

	owners := readPreparedHomeOwners(home)
	if len(owners) != 2 || owners[0] != os.Getpid() || owners[1] != liveChild {
		t.Fatalf("owners = %v, want [%d %d]", owners, os.Getpid(), liveChild)
	}
}

// TestAdoptPreparedHomeIgnoresALaunchWithoutOne guards the compatibility path: a fixed home (no live catalog) never goes through TakePreparedCleanup, so there is nothing to adopt into.
func TestAdoptPreparedHomeIgnoresALaunchWithoutOne(t *testing.T) {
	stubOwnerLiveness(t, false)
	adoptPreparedHome(liveChild)
}

// TestCondemnStalePreparedHomesReapsPastAbandonedAge covers PID reuse after a reboot, where the recorded PID reads as alive but belongs to an unrelated process.
func TestCondemnStalePreparedHomesReapsPastAbandonedAge(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, true)
	seedPreparedHome(t, root, "codex-reused-pid", abandonedPreparedHomeAge+time.Hour, liveChild)

	if condemned := condemnStalePreparedHomes(root, time.Now()); len(condemned) != 1 {
		t.Fatalf("condemned = %v, want the home whose live-looking PID is reuse", condemned)
	}
}

func TestCondemnStalePreparedHomesAgesOutUnownedHomes(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	fresh := seedPreparedHome(t, root, "codex-unowned-fresh", time.Hour)
	seedPreparedHome(t, root, "codex-unowned-old", unownedPreparedHomeAge+time.Hour)

	condemned := condemnStalePreparedHomes(root, time.Now())

	if len(condemned) != 1 || !strings.Contains(condemned[0], "codex-unowned-old") {
		t.Fatalf("condemned = %v, want only the aged-out unowned home", condemned)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("recent unowned home was reaped; a launch killed before its owner write would take a live session with it: %v", err)
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
	home := seedPreparedHome(t, root, "codex-dead", time.Minute, deadParent, deadChild)
	persistentHome := filepath.Join(filepath.Dir(root), "codex-home")
	if err := os.MkdirAll(persistentHome, 0o700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(persistentHome, "everyapi-codex-dead.config.toml")
	if err := os.WriteFile(profilePath, []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, preparedCodexProfileFile), []byte(profilePath), 0o600); err != nil {
		t.Fatal(err)
	}

	sweepStalePreparedHomes(root)

	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			if _, err := os.Stat(profilePath); !os.IsNotExist(err) {
				t.Fatalf("orphaned Codex profile survived its prepared home: %v", err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sessions root still holds %d entries after the sweep", len(entries))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSweepStalePreparedHomesDeletesCodexProfileWithoutMarker covers a hard
// kill after the persistent profile is created but before its ownership marker
// reaches the prepared home.
func TestSweepStalePreparedHomesDeletesCodexProfileWithoutMarker(t *testing.T) {
	root := preparedHomeTestRoot(t)
	stubOwnerLiveness(t, false)
	seedPreparedHome(t, root, "codex-interrupted", time.Minute, deadParent, deadChild)
	persistentHome := filepath.Join(filepath.Dir(root), "codex-home")
	if err := os.MkdirAll(persistentHome, 0o700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(persistentHome, "everyapi-codex-interrupted.config.toml")
	if err := os.WriteFile(profilePath, []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sweepStalePreparedHomes(root)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("orphaned Codex profile without a marker survived its prepared home")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTakePreparedCleanupRetainsCodexMarkerWhenProfileRemovalFails(t *testing.T) {
	root := preparedHomeTestRoot(t)
	home, err := newPreparedHome("codex")
	if err != nil {
		t.Fatal(err)
	}
	persistentHome := filepath.Join(filepath.Dir(root), "codex-home")
	profilePath := filepath.Join(persistentHome, "everyapi-"+filepath.Base(home)+".config.toml")
	if err := os.MkdirAll(filepath.Join(profilePath, "cannot-remove"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, preparedCodexProfileFile), []byte(profilePath), 0o600); err != nil {
		t.Fatal(err)
	}

	cleanup := TakePreparedCleanup(preparedHomeEnv("CODEX_HOME", home))
	if cleanup == nil {
		t.Fatal("TakePreparedCleanup returned no cleanup")
	}
	cleanup()

	if _, err := os.Stat(home); err != nil {
		t.Fatalf("prepared home was removed after profile cleanup failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, preparedCodexProfileFile)); err != nil {
		t.Fatalf("profile marker was lost after cleanup failed: %v", err)
	}
}

// TestSweepStalePreparedHomesLeavesSymlinkTargetsAlone guards the generic RemoveAll invariant: reaping a prepared home must never follow a symlink into external state.
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
	home := seedPreparedHome(t, root, "codex-linked", time.Minute, deadParent, deadChild)
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
	orphan := seedPreparedHome(t, root, "codex-dead", time.Minute, deadParent, deadChild)

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
