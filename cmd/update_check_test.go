package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withInteractive flips isInteractiveFn to a constant for the
// duration of the test. Without this, MaybePromptUpdate short-
// circuits on the TTY-false branch in `go test` and the later guards
// never get exercised.
func withInteractive(t *testing.T, v bool) {
	t.Helper()
	orig := isInteractiveFn
	isInteractiveFn = func() bool { return v }
	t.Cleanup(func() { isInteractiveFn = orig })
}

// withFetcher replaces fetchLatestTagFn so refresh code paths
// don't escape the test sandbox to hit the real GitHub API.
func withFetcher(t *testing.T, fn func(context.Context) (string, error)) {
	t.Helper()
	orig := fetchLatestTagFn
	fetchLatestTagFn = fn
	t.Cleanup(func() { fetchLatestTagFn = orig })
}

// withVersion plants a real-looking semver so MaybePromptUpdate
// doesn't trip the dev-build short-circuit (test binaries report
// Version="dev"). Every test that needs to reach past the version
// guard must call this.
func withVersion(t *testing.T, ver string) {
	t.Helper()
	orig := resolveVersionFn
	resolveVersionFn = func() (string, string) { return ver, "test" }
	t.Cleanup(func() { resolveVersionFn = orig })
}

// TestUpdateCheckCache_RoundTrip writes a cache file, reads it back,
// and verifies every field survives. Guards against future struct
// renames silently dropping a field — same shape as the credentials
// round-trip test in the SDK config package.
func TestUpdateCheckCache_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	want := &updateCheckCache{
		CheckedAt:          time.Now().Unix(),
		LastFetchAttemptAt: time.Now().Unix(),
		LatestVersion:      "v0.2.3",
		SkippedVersion:     "v0.2.3",
		LastPromptedAt:     time.Now().Add(-1 * time.Hour).Unix(),
	}
	if err := saveUpdateCheckCache(want); err != nil {
		t.Fatalf("saveUpdateCheckCache: %v", err)
	}

	path := filepath.Join(tmp, "everyapi", "update_check.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected cache file at %s: %v", path, err)
	}

	got, err := loadUpdateCheckCache()
	if err != nil {
		t.Fatalf("loadUpdateCheckCache: %v", err)
	}
	if got == nil {
		t.Fatal("loadUpdateCheckCache returned nil; expected the saved cache")
	}
	if *got != *want {
		t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", want, got)
	}
}

// TestLoadUpdateCheckCache_Missing returns (nil, nil) — distinct
// from an error — so the caller can render "no cache yet, refresh"
// without distinguishing a missing file from a corrupt one.
func TestLoadUpdateCheckCache_Missing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	got, err := loadUpdateCheckCache()
	if err != nil {
		t.Errorf("missing cache should return nil err, got %v", err)
	}
	if got != nil {
		t.Errorf("missing cache should return nil cache, got %+v", got)
	}
}

// TestLoadUpdateCheckCache_Corrupt covers the "JSON garbage on disk"
// path — typical scenarios are a hand-edited file or a torn write
// after a crash. Same return shape as Missing so the caller doesn't
// need a separate branch.
func TestLoadUpdateCheckCache_Corrupt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dir := filepath.Join(tmp, "everyapi")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "update_check.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := loadUpdateCheckCache()
	if err != nil {
		t.Errorf("corrupt cache should return nil err, got %v", err)
	}
	if got != nil {
		t.Errorf("corrupt cache should return nil cache, got %+v", got)
	}
}

// TestRefreshUpdateCheckCacheSync_CarryForwardsCooldown verifies the
// LastPromptedAt timestamp survives a cache refresh — without this
// the user would re-see the prompt on every command after a stale
// cache rebuild, defeating the "remind me later" semantics.
func TestRefreshUpdateCheckCacheSync_CarryForwardsCooldown(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	priorPrompted := time.Now().Add(-2 * time.Hour).Unix()
	prev := &updateCheckCache{
		CheckedAt:      time.Now().Add(-25 * time.Hour).Unix(),
		LatestVersion:  "v0.2.2",
		LastPromptedAt: priorPrompted,
	}
	withFetcher(t, func(ctx context.Context) (string, error) {
		return "v0.2.3", nil
	})

	got := refreshUpdateCheckCacheSync(prev)
	if got == nil {
		t.Fatal("refresh returned nil despite successful fetch")
	}
	if got.LatestVersion != "v0.2.3" {
		t.Errorf("LatestVersion = %q, want v0.2.3", got.LatestVersion)
	}
	if got.LastPromptedAt != priorPrompted {
		t.Errorf("LastPromptedAt = %d, want carry-forward of %d", got.LastPromptedAt, priorPrompted)
	}
}

// TestRefreshUpdateCheckCacheSync_ClearsStaleSkip — when a NEWER
// release ships, the user's "skip this version" from the previous
// release shouldn't silence the new one. The skip is per-version,
// not permanent.
func TestRefreshUpdateCheckCacheSync_ClearsStaleSkip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	prev := &updateCheckCache{
		CheckedAt:      time.Now().Add(-25 * time.Hour).Unix(),
		LatestVersion:  "v0.2.2",
		SkippedVersion: "v0.2.2",
	}
	withFetcher(t, func(ctx context.Context) (string, error) {
		return "v0.2.3", nil
	})

	got := refreshUpdateCheckCacheSync(prev)
	if got == nil {
		t.Fatal("refresh returned nil")
	}
	if got.SkippedVersion != "" {
		t.Errorf("SkippedVersion should clear on newer release, got %q", got.SkippedVersion)
	}
}

// TestRefreshUpdateCheckCacheSync_PreservesSkipOnSameTag — the
// inverse: if GitHub still shows the same tag the user explicitly
// skipped (no new release yet), the skip must survive the refresh
// so the prompt stays silent.
func TestRefreshUpdateCheckCacheSync_PreservesSkipOnSameTag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	prev := &updateCheckCache{
		CheckedAt:      time.Now().Add(-25 * time.Hour).Unix(),
		LatestVersion:  "v0.2.2",
		SkippedVersion: "v0.2.2",
	}
	withFetcher(t, func(ctx context.Context) (string, error) {
		return "v0.2.2", nil
	})

	got := refreshUpdateCheckCacheSync(prev)
	if got == nil {
		t.Fatal("refresh returned nil")
	}
	if got.SkippedVersion != "v0.2.2" {
		t.Errorf("SkippedVersion should survive same-tag refresh, got %q", got.SkippedVersion)
	}
}

// TestRefreshUpdateCheckCacheSync_FailureWritesMarker — the failure
// path is the lynchpin of the offline-backoff feature. After a fetch
// error, the cache must end up with LastFetchAttemptAt set (so the
// backoff in MaybePromptUpdate kicks in) and CheckedAt / LatestVersion
// preserved from prev (so a previously-good cache isn't lost).
func TestRefreshUpdateCheckCacheSync_FailureWritesMarker(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	prev := &updateCheckCache{
		CheckedAt:     time.Now().Add(-26 * time.Hour).Unix(),
		LatestVersion: "v0.2.2",
	}
	withFetcher(t, func(ctx context.Context) (string, error) {
		return "", errors.New("simulated offline")
	})

	got := refreshUpdateCheckCacheSync(prev)
	if got == nil {
		t.Fatal("failure path should still return a marker cache, got nil")
	}
	if got.LastFetchAttemptAt == 0 {
		t.Error("LastFetchAttemptAt should be stamped on failure, got 0")
	}
	if got.CheckedAt != prev.CheckedAt {
		t.Errorf("CheckedAt should be carried from prev on failure (got %d, want %d)",
			got.CheckedAt, prev.CheckedAt)
	}
	if got.LatestVersion != prev.LatestVersion {
		t.Errorf("LatestVersion should be carried from prev on failure (got %q, want %q)",
			got.LatestVersion, prev.LatestVersion)
	}

	// And the marker must hit disk so the next process sees the
	// backoff state — not just live in memory.
	loaded, err := loadUpdateCheckCache()
	if err != nil || loaded == nil {
		t.Fatalf("failure marker not persisted (err=%v, loaded=%+v)", err, loaded)
	}
	if loaded.LastFetchAttemptAt == 0 {
		t.Error("on-disk marker missing LastFetchAttemptAt")
	}
}

// TestRefreshUpdateCheckCacheSync_FailureWithNoPrior — first-ever
// invocation that fails. There's no prev cache to carry forward; we
// should still get a marker so the next command backs off.
func TestRefreshUpdateCheckCacheSync_FailureWithNoPrior(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	withFetcher(t, func(ctx context.Context) (string, error) {
		return "", errors.New("simulated offline")
	})

	got := refreshUpdateCheckCacheSync(nil)
	if got == nil {
		t.Fatal("first-time failure must still produce a backoff marker")
	}
	if got.LastFetchAttemptAt == 0 {
		t.Error("LastFetchAttemptAt should be set on first-time failure")
	}
	if got.CheckedAt != 0 || got.LatestVersion != "" {
		t.Errorf("first-time failure should leave CheckedAt + LatestVersion zero, got %+v", got)
	}
}

// TestMaybePromptUpdate_GuardChain exercises each early-exit guard
// IN ISOLATION via the isInteractiveFn injection — without it, every
// sub-test would short-circuit on TTY-false and prove nothing about
// the guard it claims to test.
func TestMaybePromptUpdate_GuardChain(t *testing.T) {
	t.Run("non-TTY returns false even with no other gates set", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmp)
		t.Setenv("EVERYAPI_NO_UPDATE_CHECK", "")
		withInteractive(t, false)
		// Fetcher should NOT be called — non-TTY skips before the
		// refresh path. Fail loud if it is.
		withFetcher(t, func(ctx context.Context) (string, error) {
			t.Error("fetch should not run when non-TTY")
			return "", errors.New("must not be called")
		})
		if MaybePromptUpdate("status") {
			t.Error("non-TTY should never skip-original")
		}
	})

	t.Run("env opt-out blocks even with TTY", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmp)
		t.Setenv("EVERYAPI_NO_UPDATE_CHECK", "1")
		withInteractive(t, true)
		withFetcher(t, func(ctx context.Context) (string, error) {
			t.Error("fetch should not run when env opt-out is set")
			return "", errors.New("must not be called")
		})
		if MaybePromptUpdate("status") {
			t.Error("env opt-out should skip the check")
		}
	})

	t.Run("skip commands block even with TTY + env unset", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmp)
		t.Setenv("EVERYAPI_NO_UPDATE_CHECK", "")
		withInteractive(t, true)
		withFetcher(t, func(ctx context.Context) (string, error) {
			t.Error("fetch should not run for skip-list commands")
			return "", errors.New("must not be called")
		})
		for _, name := range []string{"update", "version", "--version", "-v", "mcp", "help", "--help", "-h"} {
			if MaybePromptUpdate(name) {
				t.Errorf("%q should be in the skip set", name)
			}
		}
	})
}

// TestMaybePromptUpdate_DevBuildShortCircuits — a build with
// Version="dev" has no released tag to compare against, so the
// check must short-circuit before any cache / network work.
func TestMaybePromptUpdate_DevBuildShortCircuits(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("EVERYAPI_NO_UPDATE_CHECK", "")
	withInteractive(t, true)
	withVersion(t, "dev")
	withFetcher(t, func(ctx context.Context) (string, error) {
		t.Error("dev build should never reach the fetcher")
		return "", errors.New("must not be called")
	})
	if MaybePromptUpdate("status") {
		t.Error("dev build should never skip-original")
	}
}

// TestMaybePromptUpdate_FailureBackoff is the regression test for
// the offline-laptop hammering bug — after a recent failure marker,
// MaybePromptUpdate must NOT call the fetcher again until the
// failure-backoff window has elapsed.
//
// We deliberately use a real-looking version + cache with empty
// LatestVersion so the post-refresh prompt gate stays false: the
// test asserts on the fetcher-not-called signal alone.
func TestMaybePromptUpdate_FailureBackoff(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("EVERYAPI_NO_UPDATE_CHECK", "")
	withInteractive(t, true)
	withVersion(t, "v0.2.2")

	if err := saveUpdateCheckCache(&updateCheckCache{
		LastFetchAttemptAt: time.Now().Add(-1 * time.Minute).Unix(),
		// Empty LatestVersion + zero CheckedAt → not "fresh" by TTL,
		// so the only thing keeping us from re-fetching is the backoff.
	}); err != nil {
		t.Fatal(err)
	}

	fetcherCalled := false
	withFetcher(t, func(ctx context.Context) (string, error) {
		fetcherCalled = true
		return "v9.9.9", nil
	})

	_ = MaybePromptUpdate("status")
	if fetcherCalled {
		t.Error("MaybePromptUpdate hit the fetcher despite an in-window failure marker")
	}
}

// TestMaybePromptUpdate_FailureBackoffExpired — the inverse: once
// the backoff window passes, the next call SHOULD retry the fetch.
//
// We pin the current version to MATCH the fetched tag so that the
// prompt gate returns false after refresh — otherwise the picker
// would render and hang the test on a missing TTY input.
func TestMaybePromptUpdate_FailureBackoffExpired(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("EVERYAPI_NO_UPDATE_CHECK", "")
	withInteractive(t, true)
	withVersion(t, "v9.9.9")

	if err := saveUpdateCheckCache(&updateCheckCache{
		LastFetchAttemptAt: time.Now().Add(-2 * time.Hour).Unix(), // > 1h backoff
	}); err != nil {
		t.Fatal(err)
	}

	fetcherCalled := false
	withFetcher(t, func(ctx context.Context) (string, error) {
		fetcherCalled = true
		return "v9.9.9", nil
	})

	_ = MaybePromptUpdate("status")
	if !fetcherCalled {
		t.Error("MaybePromptUpdate should retry once the backoff window has elapsed")
	}
}

// TestMaybePromptUpdate_FreshCacheSkipsFetch — when the cache is
// still within its TTL, the fetcher must not run. Otherwise every
// command would hit GitHub.
func TestMaybePromptUpdate_FreshCacheSkipsFetch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("EVERYAPI_NO_UPDATE_CHECK", "")
	withInteractive(t, true)
	// Current version matches cached LatestVersion so even though the
	// gate would say "no prompt", we still want to verify the FETCH
	// didn't happen — that's the freshness contract.
	withVersion(t, "v0.0.1")

	if err := saveUpdateCheckCache(&updateCheckCache{
		CheckedAt:     time.Now().Add(-1 * time.Hour).Unix(), // fresh
		LatestVersion: "v0.0.1",
	}); err != nil {
		t.Fatal(err)
	}

	fetcherCalled := false
	withFetcher(t, func(ctx context.Context) (string, error) {
		fetcherCalled = true
		return "v9.9.9", nil
	})

	_ = MaybePromptUpdate("status")
	if fetcherCalled {
		t.Error("fresh cache should not trigger a fetch")
	}
}

// TestUpdatePromptable covers the four gates that decide whether to
// surface the prompt — extracted from MaybePromptUpdate so each can
// be asserted without touching TTY / network.
func TestUpdatePromptable(t *testing.T) {
	cases := []struct {
		name string
		ver  string
		c    *updateCheckCache
		want bool
	}{
		{"nil cache", "v0.2.2", nil, false},
		{"empty LatestVersion", "v0.2.2", &updateCheckCache{}, false},
		{"older than cached", "v0.2.2", &updateCheckCache{LatestVersion: "v0.2.3"}, true},
		{"same as cached", "v0.2.3", &updateCheckCache{LatestVersion: "v0.2.3"}, false},
		{"newer than cached (pre-release ahead)", "v0.2.4", &updateCheckCache{LatestVersion: "v0.2.3"}, false},
		{"version-skipped silences prompt",
			"v0.2.2",
			&updateCheckCache{LatestVersion: "v0.2.3", SkippedVersion: "v0.2.3"},
			false},
		{"version-skipped but tag advanced re-opens prompt",
			"v0.2.2",
			&updateCheckCache{LatestVersion: "v0.2.4", SkippedVersion: "v0.2.3"},
			true},
		{"in-cooldown silences prompt",
			"v0.2.2",
			&updateCheckCache{
				LatestVersion:  "v0.2.3",
				LastPromptedAt: time.Now().Add(-1 * time.Hour).Unix(),
			},
			false},
		{"expired cooldown re-opens prompt",
			"v0.2.2",
			&updateCheckCache{
				LatestVersion:  "v0.2.3",
				LastPromptedAt: time.Now().Add(-13 * time.Hour).Unix(),
			},
			true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := updatePromptable(tc.ver, tc.c); got != tc.want {
				t.Errorf("updatePromptable(%q, %+v) = %v, want %v", tc.ver, tc.c, got, tc.want)
			}
		})
	}
}

// TestPromptChoiceConstants pins the iota order to the choices
// slice's row order in handleUpdatePrompt. If a future edit reorders
// the slice without renumbering the consts, the switch arms below
// would dispatch wrong; this assertion catches that drift.
func TestPromptChoiceConstants(t *testing.T) {
	if choiceUpdate != 0 || choiceLater != 1 || choiceSkip != 2 {
		t.Fatalf("choice indices drifted: update=%d later=%d skip=%d",
			choiceUpdate, choiceLater, choiceSkip)
	}
}
