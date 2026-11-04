package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	cleanTestClaudeOld    = "11111111-1111-4111-8111-111111111111"
	cleanTestClaudeRecent = "22222222-2222-4222-8222-222222222222"
	cleanTestClaudeLive   = "33333333-3333-4333-8333-333333333333"
	cleanTestPiOld        = "2026-01-02T03-04-05-678Z_01a06b87-5a31-79ba-9f94-a8164ab6b266"
	cleanTestPiNested     = "2026-01-03T03-04-05-678Z_01a06bd6-15eb-75a5-b8fd-ccc0fefd00a1"
	cleanTestCodexOld     = "019cdd88-54b8-7573-a1a5-9964b6cf2648"
	cleanTestCodexArchive = "019cdd88-0000-7000-8000-000000000001"
	cleanTestCodexLive    = "019cdd88-0000-7000-8000-000000000002"
)

func writeCleanTestFile(t *testing.T, path, content string, modified time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func assertCleanTestPath(t *testing.T, path string, wantExists bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if exists := err == nil; exists != wantExists {
		t.Fatalf("%s exists = %v, want %v", path, exists, wantExists)
	}
}

func codexTestRollout(home, dir, id string) string {
	return filepath.Join(home, ".codex", dir, "rollout-2026-01-02T03-04-05-"+id+".jsonl")
}

// cleanTestHome lays out a home directory holding Claude Code, Codex and Pi sessions of different ages next to the files a careless cleaner would hit: Claude memory, a stray JSON file, a symlinked transcript, Codex's own databases and Pi's subagent artifacts.
func cleanTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	now := time.Now()
	old := now.Add(-60 * 24 * time.Hour)
	recent := now.Add(-2 * 24 * time.Hour)
	live := now.Add(-time.Minute)

	claude := filepath.Join(home, ".claude")
	project := filepath.Join(claude, "projects", "-workspace-app")
	writeCleanTestFile(t, filepath.Join(project, cleanTestClaudeOld+".jsonl"), `{"sessionId":"`+cleanTestClaudeOld+`","cwd":"/workspace/app","type":"user","message":{"role":"user","content":"Old task"}}`, old)
	writeCleanTestFile(t, filepath.Join(project, cleanTestClaudeOld, "subagents", "agent-a.jsonl"), `{"type":"user"}`, old)
	writeCleanTestFile(t, filepath.Join(project, "."+cleanTestClaudeOld+".everyapi-recovery"), cleanTestClaudeRecent+"\n", old)
	writeCleanTestFile(t, filepath.Join(claude, "file-history", cleanTestClaudeOld, "abc@v1"), "backup", old)
	writeCleanTestFile(t, filepath.Join(claude, "session-env", cleanTestClaudeOld, "env"), "env", old)
	writeCleanTestFile(t, filepath.Join(project, cleanTestClaudeRecent+".jsonl"), `{"sessionId":"`+cleanTestClaudeRecent+`"}`, recent)
	writeCleanTestFile(t, filepath.Join(claude, "file-history", cleanTestClaudeRecent, "abc@v1"), "backup", recent)
	writeCleanTestFile(t, filepath.Join(project, cleanTestClaudeLive+".jsonl"), `{"sessionId":"`+cleanTestClaudeLive+`"}`, live)
	writeCleanTestFile(t, filepath.Join(project, "memory", "MEMORY.md"), "memory", old)
	writeCleanTestFile(t, filepath.Join(project, "triage-2026-10-09.json"), `{"not":"a session"}`, old)
	writeCleanTestFile(t, filepath.Join(project, "notes.jsonl"), `{"not":"a session"}`, old)
	writeCleanTestFile(t, filepath.Join(claude, "history.jsonl"), `{"display":"prompt"}`, old)
	outside := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	writeCleanTestFile(t, outside, `{"type":"user"}`, old)
	if err := os.Symlink(outside, filepath.Join(project, "44444444-4444-4444-8444-444444444444.jsonl")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	pi := filepath.Join(home, ".pi", "agent", "sessions")
	writeCleanTestFile(t, filepath.Join(pi, cleanTestPiOld+".jsonl"), `{"type":"session"}`, old)
	nested := filepath.Join(pi, "--workspace-app--")
	writeCleanTestFile(t, filepath.Join(nested, cleanTestPiNested+".jsonl"), `{"type":"session"}`, old)
	writeCleanTestFile(t, filepath.Join(nested, cleanTestPiNested, "run-0", "session.jsonl"), `{"type":"session"}`, old)
	writeCleanTestFile(t, filepath.Join(nested, "subagent-artifacts", "outputs", "context.md"), "artifact", old)
	writeCleanTestFile(t, filepath.Join(nested, "unrelated.jsonl"), `{"type":"session"}`, old)

	oldRollout := codexTestRollout(home, filepath.Join("sessions", "2026", "01", "02"), cleanTestCodexOld)
	writeCleanTestFile(t, oldRollout, `{"type":"session_meta","payload":{"id":"`+cleanTestCodexOld+`","cwd":"/workspace/app"}}`+"\n"+`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Codex task"}]}}`, old)
	writeCleanTestFile(t, oldRollout+".zst", "compressed", old)
	writeCleanTestFile(t, codexTestRollout(home, "archived_sessions", cleanTestCodexArchive), `{"type":"session_meta"}`, old)
	writeCleanTestFile(t, codexTestRollout(home, filepath.Join("sessions", "2026", "11", "05"), cleanTestCodexLive), `{"type":"session_meta"}`, live)
	writeCleanTestFile(t, filepath.Join(home, ".codex", "state_5.sqlite"), "db", old)
	writeCleanTestFile(t, filepath.Join(home, ".codex", "history.jsonl"), `{"text":"prompt"}`, old)
	return home
}

// stubCodex stands in for the Codex CLI: it records which sessions it was asked to delete and removes their rollouts the way `codex delete` does.
func stubCodex(t *testing.T, home string) *[]string {
	t.Helper()
	var deleted []string
	previousFind, previousDelete := findCodexForDelete, codexDeleteSession
	findCodexForDelete = func() (string, error) { return "/stub/codex", nil }
	codexDeleteSession = func(codexPath, codexHome, sessionID string) error {
		deleted = append(deleted, sessionID)
		matches, _ := filepath.Glob(filepath.Join(home, ".codex", "*", "*", "*", "*", "*"+sessionID+".jsonl*"))
		archived, _ := filepath.Glob(filepath.Join(home, ".codex", "archived_sessions", "*"+sessionID+".jsonl*"))
		for _, path := range append(matches, archived...) {
			_ = os.Remove(path)
		}
		return nil
	}
	t.Cleanup(func() { findCodexForDelete, codexDeleteSession = previousFind, previousDelete })
	return &deleted
}

func TestDiscoverCleanableAgentSessionsMatchesOnlyRealSessionRecords(t *testing.T) {
	home := cleanTestHome(t)
	sessions, err := discoverCleanableAgentSessions(defaultAgentSessionCleanRoots(home), cleanTargets)
	if err != nil {
		t.Fatal(err)
	}
	projectCleanableTitles(sessions)
	got := make(map[string]cleanableAgentSession)
	for _, session := range sessions {
		got[session.Provider+":"+session.SessionID] = session
	}
	want := []string{
		"claude:" + cleanTestClaudeOld, "claude:" + cleanTestClaudeRecent, "claude:" + cleanTestClaudeLive,
		"pi:" + cleanTestPiOld, "pi:" + cleanTestPiNested,
		"codex:" + cleanTestCodexOld, "codex:" + cleanTestCodexArchive, "codex:" + cleanTestCodexLive,
	}
	if len(got) != len(want) {
		t.Fatalf("discovered %v, want %v", keysOf(got), want)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing %s in %v", key, keysOf(got))
		}
	}
	oldClaude := got["claude:"+cleanTestClaudeOld]
	if len(oldClaude.files) != 5 || oldClaude.files[len(oldClaude.files)-1] != oldClaude.FilePath {
		t.Fatalf("claude files = %v, want four companions then the transcript last", oldClaude.files)
	}
	if oldClaude.Title != "Old task" || oldClaude.CWD == nil || *oldClaude.CWD != "/workspace/app" {
		t.Fatalf("claude projection = %#v", oldClaude)
	}
	oldCodex := got["codex:"+cleanTestCodexOld]
	if len(oldCodex.files) != 2 || !strings.HasSuffix(oldCodex.FilePath, ".jsonl") || oldCodex.Title != "Codex task" {
		t.Fatalf("codex session = %#v, want the .jsonl and .zst grouped under one id with a title", oldCodex)
	}
	if nested := got["pi:"+cleanTestPiNested]; len(nested.files) != 2 || filepath.Base(nested.files[0]) != cleanTestPiNested {
		t.Fatalf("pi files = %v", nested.files)
	}
	if sessions[0].modified.After(sessions[len(sessions)-1].modified) {
		t.Fatal("sessions are not ordered oldest first")
	}
}

func keysOf(sessions map[string]cleanableAgentSession) []string {
	keys := make([]string, 0, len(sessions))
	for key := range sessions {
		keys = append(keys, key)
	}
	return keys
}

func TestSelectCleanableAgentSessionsAppliesAgeAndActiveWindow(t *testing.T) {
	now := time.Now()
	sessions := []cleanableAgentSession{
		{FilePath: "old", modified: now.Add(-40 * 24 * time.Hour)},
		{FilePath: "recent", modified: now.Add(-2 * 24 * time.Hour)},
		{FilePath: "live", modified: now.Add(-time.Minute)},
	}
	selected, active := selectCleanableAgentSessions(sessions, now, 30*24*time.Hour, nil)
	if len(selected) != 1 || selected[0].FilePath != "old" || active != 0 {
		t.Fatalf("--older-than 30d selected %v, active %d", selected, active)
	}
	selected, active = selectCleanableAgentSessions(sessions, now, 0, nil)
	if len(selected) != 2 || active != 1 {
		t.Fatalf("no age filter selected %d, active %d; want 2 and 1", len(selected), active)
	}
}

func TestRemoveCleanableAgentSessionKeepsSessionResumedAfterScan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, cleanTestClaudeOld+".jsonl")
	companion := filepath.Join(dir, cleanTestClaudeOld)
	old := time.Now().Add(-48 * time.Hour)
	writeCleanTestFile(t, path, "{}", old)
	writeCleanTestFile(t, filepath.Join(companion, "tool.txt"), "x", old)
	session, ok := newCleanableAgentSession("claude", path, []string{companion})
	if !ok {
		t.Fatal("session not recognized")
	}
	resumed := time.Now()
	if err := os.Chtimes(path, resumed, resumed); err != nil {
		t.Fatal(err)
	}
	removed, err := removeCleanableAgentSession(session, "", "", nil)
	if err != nil || removed {
		t.Fatalf("removed = %v, err = %v; want the resumed session kept", removed, err)
	}
	assertCleanTestPath(t, path, true)
	assertCleanTestPath(t, companion, true)
}

func TestCleanClaudeAndPiDeletesOldSessionsAndTheirCompanionsOnly(t *testing.T) {
	home := cleanTestHome(t)
	if err := Clean([]string{"claude", "pi", "--older-than", "30d", "--yes"}); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(home, ".claude")
	project := filepath.Join(claude, "projects", "-workspace-app")
	pi := filepath.Join(home, ".pi", "agent", "sessions")
	for _, gone := range []string{
		filepath.Join(project, cleanTestClaudeOld+".jsonl"),
		filepath.Join(project, cleanTestClaudeOld),
		filepath.Join(project, "."+cleanTestClaudeOld+".everyapi-recovery"),
		filepath.Join(claude, "file-history", cleanTestClaudeOld),
		filepath.Join(claude, "session-env", cleanTestClaudeOld),
		filepath.Join(pi, cleanTestPiOld+".jsonl"),
		filepath.Join(pi, "--workspace-app--", cleanTestPiNested+".jsonl"),
		filepath.Join(pi, "--workspace-app--", cleanTestPiNested),
	} {
		assertCleanTestPath(t, gone, false)
	}
	for _, kept := range []string{
		filepath.Join(project, cleanTestClaudeRecent+".jsonl"),
		filepath.Join(claude, "file-history", cleanTestClaudeRecent),
		filepath.Join(project, cleanTestClaudeLive+".jsonl"),
		filepath.Join(project, "memory", "MEMORY.md"),
		filepath.Join(project, "triage-2026-10-09.json"),
		filepath.Join(project, "notes.jsonl"),
		filepath.Join(project, "44444444-4444-4444-8444-444444444444.jsonl"),
		filepath.Join(claude, "history.jsonl"),
		filepath.Join(pi, "--workspace-app--", "subagent-artifacts", "outputs", "context.md"),
		filepath.Join(pi, "--workspace-app--", "unrelated.jsonl"),
		// Codex was not named.
		codexTestRollout(home, filepath.Join("sessions", "2026", "01", "02"), cleanTestCodexOld),
	} {
		assertCleanTestPath(t, kept, true)
	}
}

func TestCleanCodexGoesThroughCodexDeleteAndKeepsLiveSessions(t *testing.T) {
	home := cleanTestHome(t)
	deleted := stubCodex(t, home)
	// chatgpt is an alias for codex.
	if err := Clean([]string{"chatgpt", "-y"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*deleted, ",") != cleanTestCodexOld+","+cleanTestCodexArchive && strings.Join(*deleted, ",") != cleanTestCodexArchive+","+cleanTestCodexOld {
		t.Fatalf("codex delete called for %v, want the two old sessions only", *deleted)
	}
	assertCleanTestPath(t, codexTestRollout(home, filepath.Join("sessions", "2026", "11", "05"), cleanTestCodexLive), true)
	assertCleanTestPath(t, filepath.Join(home, ".codex", "state_5.sqlite"), true)
	assertCleanTestPath(t, filepath.Join(home, ".codex", "history.jsonl"), true)
	// Claude Code was not named.
	assertCleanTestPath(t, filepath.Join(home, ".claude", "projects", "-workspace-app", cleanTestClaudeOld+".jsonl"), true)
}

func TestCleanAllFailsUpFrontWithoutAUsableCodex(t *testing.T) {
	home := cleanTestHome(t)
	previous := findCodexForDelete
	findCodexForDelete = func() (string, error) { return "", errors.New("this Codex CLI has no `delete` command") }
	t.Cleanup(func() { findCodexForDelete = previous })
	err := Clean([]string{"all", "--yes"})
	if err == nil || !strings.Contains(err.Error(), "delete") {
		t.Fatalf("err = %v, want the missing codex delete reported", err)
	}
	// Nothing else may be deleted before the Codex check fails.
	assertCleanTestPath(t, filepath.Join(home, ".claude", "projects", "-workspace-app", cleanTestClaudeOld+".jsonl"), true)
	assertCleanTestPath(t, filepath.Join(home, ".pi", "agent", "sessions", cleanTestPiOld+".jsonl"), true)
}

func TestCleanDeletesNothingWithoutConfirmationOrOnDryRun(t *testing.T) {
	home := cleanTestHome(t)
	deleted := stubCodex(t, home)
	oldSession := filepath.Join(home, ".claude", "projects", "-workspace-app", cleanTestClaudeOld+".jsonl")

	// go test runs without a terminal, which is exactly the piped / scripted case that must not mass-delete silently.
	err := Clean([]string{"all"})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want a refusal that points at --yes", err)
	}
	if err := Clean([]string{"all", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if err := Clean([]string{"--format", "json", "all", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	assertCleanTestPath(t, oldSession, true)
	if len(*deleted) != 0 {
		t.Fatalf("codex delete called on a refused or dry run: %v", *deleted)
	}
}

func TestCleanRejectsAmbiguousOrUnknownRequests(t *testing.T) {
	cleanTestHome(t)
	cases := []struct {
		args []string
		want string
	}{
		{nil, "name what to clean"},
		{[]string{"--yes"}, "name what to clean"},
		{[]string{"gemini"}, "choose from"},
		{[]string{"claude", "--older-than", "0d"}, "positive"},
		{[]string{"claude", "--older-than", "soon"}, "invalid --older-than"},
		{[]string{"claude", "--format", "yaml"}, "unsupported clean format"},
		{[]string{"claude", "--bogus"}, "flag provided but not defined"},
	}
	for _, tc := range cases {
		err := Clean(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want it to mention %q", tc.args, err, tc.want)
		}
	}
}

func TestParseCleanTargets(t *testing.T) {
	cases := map[string]string{
		"claude":        "claude",
		"codex":         "codex",
		"ChatGPT":       "codex",
		"pi claude":     "claude,pi",
		"all":           "claude,codex,pi",
		"codex all":     "claude,codex,pi",
		"codex chatgpt": "codex",
	}
	for input, want := range cases {
		got, err := parseCleanTargets(strings.Fields(input))
		if err != nil || strings.Join(got, ",") != want {
			t.Errorf("parseCleanTargets(%q) = %v, %v; want %s", input, got, err, want)
		}
	}
}

func TestParseSessionAge(t *testing.T) {
	cases := map[string]time.Duration{
		"30d": 30 * 24 * time.Hour,
		"2w":  14 * 24 * time.Hour,
		"12h": 12 * time.Hour,
		"90m": 90 * time.Minute,
	}
	for input, want := range cases {
		got, err := parseSessionAge(input)
		if err != nil || got != want {
			t.Errorf("parseSessionAge(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
	for _, input := range []string{"", " ", "d", "-1d", "0h", "1.5d", "abc", "300000d", "9223372036854775807w"} {
		if _, err := parseSessionAge(input); err == nil {
			t.Errorf("parseSessionAge(%q) accepted an invalid age", input)
		}
	}
}

func TestCodexDeleteUsagePatternRecognizesOnlyTheDeleteSubcommand(t *testing.T) {
	if !codexDeleteUsagePattern.MatchString("Permanently delete a saved session\n\nUsage: codex delete [OPTIONS] <SESSION>\n") {
		t.Fatal("codex 0.166 delete help not recognized")
	}
	// An older Codex treats `delete` as a prompt and prints its top-level help, which lists commands but whose usage line names none.
	if codexDeleteUsagePattern.MatchString("Codex CLI\n\nUsage: codex [OPTIONS] [PROMPT]\n       codex [OPTIONS] <COMMAND> [ARGS]\n\nCommands:\n  delete  Permanently delete a saved session\n") {
		t.Fatal("top-level help mistaken for the delete subcommand")
	}
}

// registerClaudeProcess writes the entry Claude Code keeps for a running process in <claudeHome>/sessions/<pid>.json.
func registerClaudeProcess(t *testing.T, home string, pid int, sessionID string) {
	t.Helper()
	path := filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json")
	writeCleanTestFile(t, path, `{"pid":`+strconv.Itoa(pid)+`,"sessionId":"`+sessionID+`","kind":"interactive","status":"idle"}`, time.Now())
}

func reapedPID(t *testing.T) int {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^$")
	if err := command.Run(); err != nil {
		t.Fatalf("run short-lived helper: %v", err)
	}
	return command.Process.Pid
}

func TestCleanKeepsClaudeSessionsOpenInARunningProcessHoweverIdle(t *testing.T) {
	home := cleanTestHome(t)
	// The old session is idle for 60 days but still open in a live Claude Code — the test process stands in for it. The recent one names a process that has exited, so its registration protects nothing.
	registerClaudeProcess(t, home, os.Getpid(), cleanTestClaudeOld)
	registerClaudeProcess(t, home, reapedPID(t), cleanTestClaudeRecent)
	if err := Clean([]string{"claude", "--yes"}); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(home, ".claude")
	project := filepath.Join(claude, "projects", "-workspace-app")
	assertCleanTestPath(t, filepath.Join(project, cleanTestClaudeOld+".jsonl"), true)
	assertCleanTestPath(t, filepath.Join(project, cleanTestClaudeOld), true)
	assertCleanTestPath(t, filepath.Join(claude, "file-history", cleanTestClaudeOld), true)
	assertCleanTestPath(t, filepath.Join(project, cleanTestClaudeRecent+".jsonl"), false)
	// The registry itself belongs to Claude Code and is never cleaned.
	assertCleanTestPath(t, filepath.Join(claude, "sessions", strconv.Itoa(os.Getpid())+".json"), true)
}

func TestRemoveCleanableAgentSessionKeepsClaudeSessionOpenedAfterScan(t *testing.T) {
	home := cleanTestHome(t)
	sessions, err := discoverCleanableAgentSessions(defaultAgentSessionCleanRoots(home), []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := selectCleanableAgentSessions(sessions, time.Now(), 0, liveClaudeSessionIDs(filepath.Join(home, ".claude")))
	var target cleanableAgentSession
	for _, session := range selected {
		if session.SessionID == cleanTestClaudeOld {
			target = session
		}
	}
	if target.SessionID == "" {
		t.Fatal("old session not selected before it was opened")
	}
	// Opened while the confirmation prompt was up: the second registry read must catch it.
	registerClaudeProcess(t, home, os.Getpid(), cleanTestClaudeOld)
	removed, err := removeCleanableAgentSession(target, "", "", liveClaudeSessionIDs(filepath.Join(home, ".claude")))
	if err != nil || removed {
		t.Fatalf("removed = %v, err = %v; want the newly opened session kept", removed, err)
	}
	assertCleanTestPath(t, target.FilePath, true)
}
