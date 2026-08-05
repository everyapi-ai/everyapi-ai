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
	origNotice := claudeUpdateNoticeFn
	t.Cleanup(func() {
		claudeUpdateInteractiveFn = origInteractive
		installedClaudeVersionFn = origInstalled
		latestClaudeVersionFn = origLatest
		claudeUpdateNoticeFn = origNotice
	})

	claudeUpdateInteractiveFn = func() bool { return true }
	installedClaudeVersionFn = func() (string, error) { return "2.1.211", nil }
	latestClaudeVersionFn = func() (string, error) { return "2.1.222", nil }

	notices := 0
	claudeUpdateNoticeFn = func(current, latest string) {
		notices++
		if current != "2.1.211" || latest != "2.1.222" {
			t.Errorf("notice versions = %q -> %q", current, latest)
		}
	}

	maybeNotifyClaudeUpdate("codex")
	if notices != 0 {
		t.Fatalf("non-Claude launch emitted %d notices", notices)
	}

	maybeNotifyClaudeUpdate("claude")
	if notices != 1 {
		t.Fatalf("outdated Claude launch emitted %d notices, want 1", notices)
	}

	maybeNotifyClaudeUpdate("claude")
	if notices != 1 {
		t.Fatalf("same Claude release ignored cooldown; notices = %d", notices)
	}
}

func TestMaybeNotifyClaudeUpdateFailsOpen(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	origInteractive := claudeUpdateInteractiveFn
	origInstalled := installedClaudeVersionFn
	origLatest := latestClaudeVersionFn
	origNotice := claudeUpdateNoticeFn
	t.Cleanup(func() {
		claudeUpdateInteractiveFn = origInteractive
		installedClaudeVersionFn = origInstalled
		latestClaudeVersionFn = origLatest
		claudeUpdateNoticeFn = origNotice
	})

	claudeUpdateInteractiveFn = func() bool { return true }
	installedClaudeVersionFn = func() (string, error) { return "", errors.New("version command failed") }
	latestClaudeVersionFn = func() (string, error) { return "", errors.New("offline") }
	claudeUpdateNoticeFn = func(_, _ string) { t.Fatal("failed check must not notify") }

	maybeNotifyClaudeUpdate("claude")
}

func TestMaybeNotifyClaudeUpdateBacksOffAfterLatestVersionFetchFails(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	origInteractive := claudeUpdateInteractiveFn
	origInstalled := installedClaudeVersionFn
	origLatest := latestClaudeVersionFn
	origNotice := claudeUpdateNoticeFn
	t.Cleanup(func() {
		claudeUpdateInteractiveFn = origInteractive
		installedClaudeVersionFn = origInstalled
		latestClaudeVersionFn = origLatest
		claudeUpdateNoticeFn = origNotice
	})

	claudeUpdateInteractiveFn = func() bool { return true }
	installedClaudeVersionFn = func() (string, error) { return "2.1.211", nil }
	fetches := 0
	latestClaudeVersionFn = func() (string, error) {
		fetches++
		return "", errors.New("offline")
	}
	claudeUpdateNoticeFn = func(_, _ string) { t.Fatal("failed fetch must not notify") }

	maybeNotifyClaudeUpdate("claude")
	maybeNotifyClaudeUpdate("claude")
	if fetches != 1 {
		t.Fatalf("latest-version fetches = %d, want 1 during failure backoff", fetches)
	}
}
