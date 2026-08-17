package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestLatestClaudeVersionFallsBackToMirror(t *testing.T) {
	officialCalls := 0
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		officialCalls++
		http.Error(w, "blocked", http.StatusBadGateway)
	}))
	defer official.Close()
	mirrorCalls := 0
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mirrorCalls++
		_, _ = w.Write([]byte("2.1.233\n"))
	}))
	defer mirror.Close()

	originalURLs := claudeLatestVersionURLs
	claudeLatestVersionURLs = []string{official.URL, mirror.URL}
	t.Cleanup(func() { claudeLatestVersionURLs = originalURLs })

	version, err := latestClaudeVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != "2.1.233" {
		t.Fatalf("latest version = %q, want 2.1.233", version)
	}
	if officialCalls != 1 || mirrorCalls != 1 {
		t.Fatalf("calls = official %d, mirror %d; want 1 each", officialCalls, mirrorCalls)
	}
}

func TestLatestClaudeVersionReportsAllSourcesInvalid(t *testing.T) {
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-a-version"))
	}))
	defer invalid.Close()

	originalURLs := claudeLatestVersionURLs
	claudeLatestVersionURLs = []string{invalid.URL, invalid.URL}
	t.Cleanup(func() { claudeLatestVersionURLs = originalURLs })

	if _, err := latestClaudeVersion(); err == nil || !strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("latestClaudeVersion() error = %v, want invalid-version error", err)
	}
}

func TestRunClaudeUpdateFallsBackToReviewedInstaller(t *testing.T) {
	originalCommand := claudeUpdateCommandFn
	originalMirror := claudeMirrorUpdateFn
	t.Cleanup(func() {
		claudeUpdateCommandFn = originalCommand
		claudeMirrorUpdateFn = originalMirror
	})

	commandCalls := 0
	mirrorCalls := 0
	claudeUpdateCommandFn = func() error {
		commandCalls++
		return errors.New("official download blocked")
	}
	claudeMirrorUpdateFn = func() error {
		mirrorCalls++
		return nil
	}

	if err := runClaudeUpdate(); err != nil {
		t.Fatal(err)
	}
	if commandCalls != 1 || mirrorCalls != 1 {
		t.Fatalf("calls = command %d, mirror %d; want 1 each", commandCalls, mirrorCalls)
	}
}

func TestRunClaudeUpdateCombinesOfficialAndMirrorFailures(t *testing.T) {
	originalCommand := claudeUpdateCommandFn
	originalMirror := claudeMirrorUpdateFn
	t.Cleanup(func() {
		claudeUpdateCommandFn = originalCommand
		claudeMirrorUpdateFn = originalMirror
	})

	claudeUpdateCommandFn = func() error { return errors.New("official failed") }
	claudeMirrorUpdateFn = func() error { return errors.New("mirror failed") }
	err := runClaudeUpdate()
	if err == nil || !strings.Contains(err.Error(), "official failed") || !strings.Contains(err.Error(), "mirror failed") {
		t.Fatalf("runClaudeUpdate() error = %v, want both failures", err)
	}
}

func TestRunClaudeUpdateDoesNotFallbackAfterUserInterrupt(t *testing.T) {
	originalCommand := claudeUpdateCommandFn
	originalMirror := claudeMirrorUpdateFn
	t.Cleanup(func() {
		claudeUpdateCommandFn = originalCommand
		claudeMirrorUpdateFn = originalMirror
	})

	claudeUpdateCommandFn = func() error { return errClaudeUpdateInterrupted }
	claudeMirrorUpdateFn = func() error {
		t.Fatal("user interrupt must not start a second installer")
		return nil
	}
	if err := runClaudeUpdate(); !errors.Is(err, errClaudeUpdateInterrupted) {
		t.Fatalf("runClaudeUpdate() error = %v, want interrupted", err)
	}
}

func TestRunClaudeUpdateCommandCancelsAChildThatIgnoresInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signal contract")
	}
	bin := t.TempDir()
	ready := filepath.Join(bin, "ready")
	claude := filepath.Join(bin, "claude")
	script := "#!/bin/sh\ntrap '' INT\nprintf ready >\"$FAKE_CLAUDE_READY\"\nexec sleep 30\n"
	if err := os.WriteFile(claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_CLAUDE_READY", ready)

	done := make(chan error, 1)
	go func() { done <- runClaudeUpdateCommand() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake Claude update did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errClaudeUpdateInterrupted) {
			t.Fatalf("runClaudeUpdateCommand() error = %v, want interrupted", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("interrupted Claude update child was not cancelled")
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
