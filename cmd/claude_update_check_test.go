package cmd

import (
	"errors"
	"testing"
	"time"
)

func TestParseClaudeVersion(t *testing.T) {
	cases := []struct {
		output string
		want   string
	}{
		{"2.1.211 (Claude Code)\n", "2.1.211"},
		{"claude version 2.1.222", "2.1.222"},
		{"v2.1.222", "2.1.222"},
		{"unexpected output", ""},
	}
	for _, tc := range cases {
		if got := parseClaudeVersion(tc.output); got != tc.want {
			t.Errorf("parseClaudeVersion(%q) = %q, want %q", tc.output, got, tc.want)
		}
	}
}

func TestClaudeUpdatePromptable(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name    string
		current string
		latest  string
		cache   *claudeUpdateCheckCache
		want    bool
	}{
		{"newer release", "2.1.211", "2.1.222", nil, true},
		{"same release", "2.1.222", "2.1.222", nil, false},
		{"installed release is newer", "2.1.223", "2.1.222", nil, false},
		{"same release already prompted", "2.1.211", "2.1.222", &claudeUpdateCheckCache{NotifiedVersion: "2.1.222", LastNotifiedAt: now.Add(-time.Hour).Unix()}, false},
		{"same release cooldown expired", "2.1.211", "2.1.222", &claudeUpdateCheckCache{NotifiedVersion: "2.1.222", LastNotifiedAt: now.Add(-13 * time.Hour).Unix()}, true},
		{"new release bypasses old cooldown", "2.1.211", "2.1.223", &claudeUpdateCheckCache{NotifiedVersion: "2.1.222", LastNotifiedAt: now.Add(-time.Hour).Unix()}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeUpdatePromptable(tc.current, tc.latest, tc.cache, now); got != tc.want {
				t.Errorf("claudeUpdatePromptable(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestMaybeNotifyClaudeUpdate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("EVERYAPI_NO_UPDATE_CHECK", "")

	origInteractive := claudeUpdateInteractiveFn
	origInstalled := installedClaudeVersionFn
	origLatest := latestClaudeVersionFn
	origPrompt := claudeUpdatePromptFn
	origRun := runClaudeUpdateFn
	t.Cleanup(func() {
		claudeUpdateInteractiveFn = origInteractive
		installedClaudeVersionFn = origInstalled
		latestClaudeVersionFn = origLatest
		claudeUpdatePromptFn = origPrompt
		runClaudeUpdateFn = origRun
	})

	claudeUpdateInteractiveFn = func() bool { return true }
	installedClaudeVersionFn = func() (string, error) { return "2.1.211", nil }
	latestClaudeVersionFn = func() (string, error) { return "2.1.222", nil }

	prompts := 0
	claudeUpdatePromptFn = func(current, latest string) (bool, error) {
		prompts++
		if current != "2.1.211" || latest != "2.1.222" {
			t.Errorf("prompt versions = %q -> %q", current, latest)
		}
		return false, nil
	}
	runClaudeUpdateFn = func() error { t.Fatal("later choice must not update"); return nil }

	maybeNotifyClaudeUpdate("codex")
	if prompts != 0 {
		t.Fatalf("non-Claude launch emitted %d prompts", prompts)
	}

	maybeNotifyClaudeUpdate("claude")
	if prompts != 1 {
		t.Fatalf("outdated Claude launch emitted %d prompts, want 1", prompts)
	}

	maybeNotifyClaudeUpdate("claude")
	if prompts != 1 {
		t.Fatalf("same Claude release ignored cooldown; prompts = %d", prompts)
	}
}

func TestMaybeNotifyClaudeUpdateRunsConfirmedUpdate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	origInteractive := claudeUpdateInteractiveFn
	origInstalled := installedClaudeVersionFn
	origLatest := latestClaudeVersionFn
	origPrompt := claudeUpdatePromptFn
	origRun := runClaudeUpdateFn
	t.Cleanup(func() {
		claudeUpdateInteractiveFn = origInteractive
		installedClaudeVersionFn = origInstalled
		latestClaudeVersionFn = origLatest
		claudeUpdatePromptFn = origPrompt
		runClaudeUpdateFn = origRun
	})

	claudeUpdateInteractiveFn = func() bool { return true }
	installedClaudeVersionFn = func() (string, error) { return "2.1.211", nil }
	latestClaudeVersionFn = func() (string, error) { return "2.1.222", nil }
	claudeUpdatePromptFn = func(current, latest string) (bool, error) { return true, nil }
	runs := 0
	runClaudeUpdateFn = func() error { runs++; return nil }

	maybeNotifyClaudeUpdate("claude")
	if runs != 1 {
		t.Fatalf("confirmed update ran %d times, want 1", runs)
	}
}

func TestMaybeNotifyClaudeUpdateReportsFailureAndReturnsToLaunch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	origInteractive := claudeUpdateInteractiveFn
	origInstalled := installedClaudeVersionFn
	origLatest := latestClaudeVersionFn
	origPrompt := claudeUpdatePromptFn
	origRun := runClaudeUpdateFn
	origError := claudeUpdateErrorFn
	t.Cleanup(func() {
		claudeUpdateInteractiveFn = origInteractive
		installedClaudeVersionFn = origInstalled
		latestClaudeVersionFn = origLatest
		claudeUpdatePromptFn = origPrompt
		runClaudeUpdateFn = origRun
		claudeUpdateErrorFn = origError
	})

	claudeUpdateInteractiveFn = func() bool { return true }
	installedClaudeVersionFn = func() (string, error) { return "2.1.211", nil }
	latestClaudeVersionFn = func() (string, error) { return "2.1.222", nil }
	claudeUpdatePromptFn = func(_, _ string) (bool, error) { return true, nil }
	runClaudeUpdateFn = func() error { return errors.New("updater failed") }
	reported := 0
	claudeUpdateErrorFn = func(err error) {
		reported++
		if err.Error() != "updater failed" {
			t.Errorf("reported error = %v", err)
		}
	}

	maybeNotifyClaudeUpdate("claude")
	if reported != 1 {
		t.Fatalf("update failures reported %d times, want 1", reported)
	}
}

func TestMaybeNotifyClaudeUpdateFailsOpen(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	origInteractive := claudeUpdateInteractiveFn
	origInstalled := installedClaudeVersionFn
	origLatest := latestClaudeVersionFn
	origPrompt := claudeUpdatePromptFn
	t.Cleanup(func() {
		claudeUpdateInteractiveFn = origInteractive
		installedClaudeVersionFn = origInstalled
		latestClaudeVersionFn = origLatest
		claudeUpdatePromptFn = origPrompt
	})

	claudeUpdateInteractiveFn = func() bool { return true }
	installedClaudeVersionFn = func() (string, error) { return "", errors.New("version command failed") }
	latestClaudeVersionFn = func() (string, error) { return "", errors.New("offline") }
	claudeUpdatePromptFn = func(_, _ string) (bool, error) {
		t.Fatal("failed check must not prompt")
		return false, nil
	}

	maybeNotifyClaudeUpdate("claude")
}

func TestMaybeNotifyClaudeUpdateBacksOffAfterLatestVersionFetchFails(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	origInteractive := claudeUpdateInteractiveFn
	origInstalled := installedClaudeVersionFn
	origLatest := latestClaudeVersionFn
	origPrompt := claudeUpdatePromptFn
	t.Cleanup(func() {
		claudeUpdateInteractiveFn = origInteractive
		installedClaudeVersionFn = origInstalled
		latestClaudeVersionFn = origLatest
		claudeUpdatePromptFn = origPrompt
	})

	claudeUpdateInteractiveFn = func() bool { return true }
	installedClaudeVersionFn = func() (string, error) { return "2.1.211", nil }
	fetches := 0
	latestClaudeVersionFn = func() (string, error) {
		fetches++
		return "", errors.New("offline")
	}
	claudeUpdatePromptFn = func(_, _ string) (bool, error) {
		t.Fatal("failed fetch must not prompt")
		return false, nil
	}

	maybeNotifyClaudeUpdate("claude")
	maybeNotifyClaudeUpdate("claude")
	if fetches != 1 {
		t.Fatalf("latest-version fetches = %d, want 1 during failure backoff", fetches)
	}
}
