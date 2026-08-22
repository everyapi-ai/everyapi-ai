//go:build !windows

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

func TestTmuxLaunchArgsPreserveUseArgumentsWithoutShellJoining(t *testing.T) {
	useArgs := []string{"claude", "--", "--model", "model with spaces", ";", "touch /tmp/nope"}
	payload, err := encodeTmuxUseArgs(useArgs)
	if err != nil {
		t.Fatal(err)
	}
	got := tmuxLaunchArgs(
		"/usr/bin/env",
		"/Applications/Every API/everyapi",
		"/tmp/project with spaces",
		"everyapi-123-456",
		payload,
		"/tmp/everyapi-status.sock",
		"/tmp/everyapi-environment.json",
		"/tmp/everyapi-tmux-abc/error.txt",
	)
	want := []string{
		"tmux", "new-session", "-s", "everyapi-123-456", "-c", "/tmp/project with spaces",
		"/usr/bin/env", tmuxUseArgsEnv + "=" + payload, tmuxStatusSocketEnv + "=/tmp/everyapi-status.sock", tmuxEnvironmentFileEnv + "=/tmp/everyapi-environment.json", tmuxErrorFileEnv + "=/tmp/everyapi-tmux-abc/error.txt", tools.TerminalModeEnvironment + "=tmux", tools.TmuxSessionEnvironment + "=everyapi-123-456", tools.TmuxAttachCommandEnvironment + "=tmux attach -t everyapi-123-456", "/Applications/Every API/everyapi", tmuxUseWrapperCommand,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux args = %#v, want %#v", got, want)
	}
	decoded, err := decodeTmuxUseArgs(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, useArgs) {
		t.Fatalf("decoded args = %#v, want %#v", decoded, useArgs)
	}
}

func TestTmuxSessionNameGroupsLaunchesByToolAndWorkspace(t *testing.T) {
	prefix := tmuxSessionPrefix("codex", "/tmp/project with spaces")
	first, err := newTmuxSessionName(prefix)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newTmuxSessionName(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("concurrent launches shared session name %q", first)
	}
	if !strings.HasPrefix(prefix, "everyapi-v3-codex-") {
		t.Fatalf("session prefix = %q, want versioned random-identity format", prefix)
	}
	for _, name := range []string{first, second} {
		if !strings.HasPrefix(name, prefix) {
			t.Fatalf("session name %q does not use workspace prefix %q", name, prefix)
		}
		nonce := strings.TrimPrefix(name, prefix)
		if len(nonce) != 32 || strings.Trim(nonce, "0123456789abcdef") != "" {
			t.Fatalf("session name %q does not end in a 128-bit hexadecimal nonce", name)
		}
		if strings.ContainsAny(name, " /\\\t\n") {
			t.Fatalf("session name is not tmux-safe: %q", name)
		}
	}
	if got := tmuxSessionPrefix("codex", "/tmp/another-project"); got == prefix {
		t.Fatalf("different workspaces shared prefix %q", prefix)
	}
	if got := tmuxSessionPrefix("claude", "/tmp/project with spaces"); got == prefix {
		t.Fatalf("different tools shared prefix %q", prefix)
	}
}

func TestTmuxSessionPrefixUsesFilesystemIdentityForAliases(t *testing.T) {
	workspaceRoot := t.TempDir()
	mixedCasePath := filepath.Join(workspaceRoot, "EveryAPIWorkspace")
	if err := os.Mkdir(mixedCasePath, 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("symlink", func(t *testing.T) {
		symlinkAlias := filepath.Join(workspaceRoot, "workspace-link")
		if err := os.Symlink(mixedCasePath, symlinkAlias); err != nil {
			t.Fatal(err)
		}
		if mixed, alias := tmuxSessionPrefix("codex", mixedCasePath), tmuxSessionPrefix("codex", symlinkAlias); mixed != alias {
			t.Fatalf("symlink aliases for one workspace produced different prefixes: %q != %q", mixed, alias)
		}
	})

	t.Run("case insensitive filesystem", func(t *testing.T) {
		lowerCaseAlias := filepath.Join(workspaceRoot, "everyapiworkspace")
		if _, err := os.Stat(lowerCaseAlias); errors.Is(err, os.ErrNotExist) {
			t.Skip("filesystem is case-sensitive")
		} else if err != nil {
			t.Fatal(err)
		}
		if mixed, alias := tmuxSessionPrefix("codex", mixedCasePath), tmuxSessionPrefix("codex", lowerCaseAlias); mixed != alias {
			t.Fatalf("case aliases for one workspace produced different prefixes: %q != %q", mixed, alias)
		}
	})
}

func tmuxTestInventoryLine(name, id string, dead, managed bool) string {
	return tmuxTestInventoryPaneLine(name, id, "%"+strings.TrimPrefix(id, "$"), dead, managed)
}

func tmuxTestInventoryPaneLine(name, id, paneID string, dead, managed bool) string {
	deadValue := "0"
	if dead {
		deadValue = "1"
	}
	managedValue := "0"
	if managed {
		managedValue = "1"
	}
	return strings.Join([]string{name, id, paneID, deadValue, managedValue}, "\t")
}

func TestClassifyTmuxSessionsReusesOnlyUniqueLiveWorkspaceAndFindsDeadGarbage(t *testing.T) {
	prefix := tmuxSessionPrefix("codex", "/tmp/project")
	matching := prefix + strings.Repeat("1", 32)
	deadMatching := prefix + strings.Repeat("2", 32)
	otherWorkspace := tmuxSessionPrefix("codex", "/tmp/other") + strings.Repeat("3", 32)
	deadOtherTool := tmuxSessionPrefix("claude", "/tmp/project") + strings.Repeat("4", 32)
	legacyDead := "everyapi-987-654321"
	previousVersionDead := "everyapi-v2-codex-0123456789ab-128-461"
	prefixedUserSession := tmuxSessionPrefix("codex", "/tmp/user") + strings.Repeat("5", 32)
	invalidV2UserSession := "everyapi-v2-codex-project"
	output := strings.Join([]string{
		tmuxTestInventoryLine(matching, "$1", false, true),
		tmuxTestInventoryLine(matching, "$1", true, false),
		tmuxTestInventoryLine(deadMatching, "$2", true, true),
		tmuxTestInventoryLine(otherWorkspace, "$3", false, true),
		tmuxTestInventoryLine(deadOtherTool, "$4", true, true),
		tmuxTestInventoryLine(legacyDead, "$5", true, true),
		tmuxTestInventoryLine(previousVersionDead, "$9", true, true),
		tmuxTestInventoryLine(prefixedUserSession, "$6", true, false),
		tmuxTestInventoryLine("user-session", "$7", true, false),
		tmuxTestInventoryLine(invalidV2UserSession, "$8", true, true),
	}, "\n") + "\n"

	gotSession, gotDead, err := classifyTmuxSessions(output, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.name != matching || gotSession.paneID != "%1" {
		t.Fatalf("reusable session = %#v, want %q / %%1", gotSession, matching)
	}
	wantDead := []tmuxSessionReference{
		{name: legacyDead, paneID: "%5"},
		{name: previousVersionDead, paneID: "%9"},
		{name: deadOtherTool, paneID: "%4"},
		{name: deadMatching, paneID: "%2"},
	}
	if !reflect.DeepEqual(gotDead, wantDead) {
		t.Fatalf("dead sessions = %#v, want %#v", gotDead, wantDead)
	}
}

func TestClassifyTmuxSessionsReusesPreviousPathIdentityDuringUpgrade(t *testing.T) {
	workspace := t.TempDir()
	currentPrefix := tmuxSessionPrefix("codex", workspace)
	previousPrefix := previousTmuxSessionPrefix("codex", workspace)
	if currentPrefix == previousPrefix {
		t.Fatal("test fixture did not produce distinct filesystem and path identities")
	}
	previousSession := previousPrefix + strings.Repeat("d", 32)
	output := tmuxTestInventoryLine(previousSession, "$1", false, true) + "\n"

	got, _, err := classifyTmuxSessions(output, currentPrefix, previousPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if got.name != previousSession {
		t.Fatalf("previous path-identity session was not reusable during upgrade: got %#v, want %q", got, previousSession)
	}
}

func TestClassifyTmuxSessionsRequiresStrictGeneratedNameBeforeCleanup(t *testing.T) {
	output := strings.Join([]string{
		tmuxTestInventoryLine("everyapi-v2-codex-project", "$1", true, true),
		tmuxTestInventoryLine("everyapi-v2-codex-0123456789ab-0-123", "$2", true, true),
		tmuxTestInventoryLine("everyapi-v2-codex-0123456789ab-123-0", "$3", true, true),
		tmuxTestInventoryLine("everyapi-v2-codex-not-a-hash-123-456", "$4", true, true),
	}, "\n") + "\n"

	gotSession, gotDead, err := classifyTmuxSessions(output, "everyapi-v2-codex-")
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.name != "" {
		t.Fatalf("invalid generated session was reused: %#v", gotSession)
	}
	if len(gotDead) != 0 {
		t.Fatalf("user sessions with invalid generated names were pruned: %#v", gotDead)
	}
}

func TestTmuxSessionConditionsRecognizeOnlyLiveManagedPanes(t *testing.T) {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTemp, err := os.MkdirTemp("/tmp", "everyapi-tmux-condition-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTemp) })
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", tmuxTemp)
	defer exec.Command(tmuxPath, "kill-server").Run() //nolint:errcheck // best-effort isolated test cleanup

	managedSession := "everyapi-v3-codex-0123456789ab-" + strings.Repeat("1", 32)
	if output, err := exec.Command(
		tmuxPath, "new-session", "-d", "-s", managedSession,
		"sh", "-c", "while :; do sleep 1; done", "sh", tmuxUseWrapperCommand,
	).CombinedOutput(); err != nil {
		t.Fatalf("start isolated managed tmux session: %v\n%s", err, output)
	}
	paneOutput, err := exec.Command(tmuxPath, "list-panes", "-t", exactTmuxSessionTarget(managedSession), "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("find managed pane: %v\n%s", err, paneOutput)
	}
	managedPaneID := strings.TrimSpace(string(paneOutput))

	formatValue := func(format string) string {
		t.Helper()
		output, err := exec.Command(tmuxPath, "display-message", "-p", "-t", managedPaneID, format).CombinedOutput()
		if err != nil {
			t.Fatalf("expand tmux format %q: %v\n%s", format, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	if got := formatValue(tmuxReusableCondition(managedSession)); got != "1" {
		t.Fatalf("live managed session reusable condition = %q, want 1", got)
	}
	if got := formatValue(tmuxDeadCleanupCondition(managedSession)); got != "0" {
		t.Fatalf("live managed session cleanup condition = %q, want 0", got)
	}
}

func TestTmuxAndConditionsUsesBinaryNestingForOlderTmux(t *testing.T) {
	if got, want := tmuxAndConditions("one", "two", "three"), "#{&&:one,#{&&:two,three}}"; got != want {
		t.Fatalf("combined tmux condition = %q, want %q", got, want)
	}
	if got := tmuxAndConditions(); got != "1" {
		t.Fatalf("empty tmux condition = %q, want 1", got)
	}
}

func TestClassifyTmuxSessionsPreservesUserPaneAfterManagedPaneDies(t *testing.T) {
	prefix := tmuxSessionPrefix("codex", "/tmp/project")
	session := prefix + strings.Repeat("1", 32)
	output := strings.Join([]string{
		tmuxTestInventoryLine(session, "$1", true, true),
		tmuxTestInventoryLine(session, "$1", false, false),
	}, "\n") + "\n"

	gotSession, gotDead, err := classifyTmuxSessions(output, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.name != "" {
		t.Fatalf("session with dead managed pane was reused: %#v", gotSession)
	}
	if len(gotDead) != 0 {
		t.Fatalf("session with live user pane was pruned: %#v", gotDead)
	}
}

func TestClassifyTmuxSessionsTargetsTheLiveManagedPane(t *testing.T) {
	prefix := tmuxSessionPrefix("codex", "/tmp/project")
	session := prefix + strings.Repeat("b", 32)
	output := strings.Join([]string{
		tmuxTestInventoryPaneLine(session, "$1", "%10", true, true),
		tmuxTestInventoryPaneLine(session, "$1", "%11", false, true),
	}, "\n") + "\n"

	gotSession, _, err := classifyTmuxSessions(output, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.paneID != "%11" {
		t.Fatalf("reusable session targets pane %q, want live managed pane %%11", gotSession.paneID)
	}
}

func TestClassifyTmuxSessionsDoesNotGuessBetweenMultipleLiveMatches(t *testing.T) {
	prefix := tmuxSessionPrefix("codex", "/tmp/project")
	first := prefix + strings.Repeat("1", 32)
	second := prefix + strings.Repeat("2", 32)
	output := tmuxTestInventoryLine(first, "$1", false, true) + "\n" + tmuxTestInventoryLine(second, "$2", false, true) + "\n"
	gotSession, gotDead, err := classifyTmuxSessions(output, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.name != "" {
		t.Fatalf("ambiguous live sessions selected %#v", gotSession)
	}
	if len(gotDead) != 0 {
		t.Fatalf("live sessions classified as dead: %#v", gotDead)
	}
}

func TestClassifyTmuxSessionsRejectsMalformedInventory(t *testing.T) {
	prefix := tmuxSessionPrefix("codex", "/tmp/project")
	for _, output := range []string{"missing-tabs\n", "session\t$1\t%1\t2\t1\n", "\t$1\t%1\t0\t1\n"} {
		if _, _, err := classifyTmuxSessions(output, prefix); err == nil {
			t.Fatalf("malformed inventory accepted: %q", output)
		}
	}
}

func TestReusableTmuxSessionPrunesOnlyManagedDeadSessions(t *testing.T) {
	prefix := tmuxSessionPrefix("codex", "/tmp/project")
	live := prefix + strings.Repeat("1", 32)
	dead := tmuxSessionPrefix("claude", "/tmp/other") + strings.Repeat("2", 32)
	testDir := t.TempDir()
	logPath := filepath.Join(testDir, "tmux.log")
	tmuxPath := filepath.Join(testDir, "tmux")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_TEST_LOG"
if [ "$1" = "list-panes" ]; then
  printf '%s\t$1\t%%1\t0\t1\n%s\t$2\t%%2\t1\t1\n%s\t$3\t%%3\t1\t0\n' "$TMUX_TEST_LIVE" "$TMUX_TEST_DEAD" "user-session"
fi
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TEST_LOG", logPath)
	t.Setenv("TMUX_TEST_LIVE", live)
	t.Setenv("TMUX_TEST_DEAD", dead)

	got, err := reusableTmuxSession(tmuxPath, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if got.name != live || got.paneID != "%1" {
		t.Fatalf("reusable session = %#v, want %q / %%1", got, live)
	}
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBody)
	deadCleanupLine := "if-shell -F -t %2"
	if !strings.Contains(logText, deadCleanupLine) || !strings.Contains(logText, "kill-session -t ="+dead) {
		t.Fatalf("dead managed session was not atomically revalidated and pruned by exact name:\n%s", logText)
	}
	if strings.Contains(logText, "kill-session -t $2\n") {
		t.Fatalf("stale server-local session ID was used for cleanup:\n%s", logText)
	}
	for _, protected := range []string{"$1", "$3", live, "user-session"} {
		if strings.Contains(logText, "kill-session -t "+protected) {
			t.Fatalf("live or unmanaged session %q was pruned:\n%s", protected, logText)
		}
	}
}

func TestAttachReusableTmuxSessionUsesStableServerIdentity(t *testing.T) {
	testDir := t.TempDir()
	session := tmuxSessionReference{
		name:   tmuxSessionPrefix("codex", "/tmp/project") + strings.Repeat("1", 32),
		paneID: "%42",
	}
	logPath := filepath.Join(testDir, "tmux.log")
	tmuxPath := filepath.Join(testDir, "tmux")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_TEST_LOG"
if [ "$1" = "if-shell" ]; then
  exit 0
fi
exit 2
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TEST_LOG", logPath)
	attached, err := attachReusableTmuxSession(tmuxPath, session)
	if err != nil {
		t.Fatal(err)
	}
	if !attached {
		t.Fatal("successful tmux attach was not handled")
	}
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBody)
	if !strings.Contains(logText, "if-shell -F -t "+session.paneID) || !strings.Contains(logText, "attach-session -t ="+session.name) {
		t.Fatalf("attach did not atomically revalidate and target the exact session name:\n%s", logText)
	}
	if strings.Contains(logText, "$42") {
		t.Fatalf("attach used a reusable server-local ID:\n%s", logText)
	}
	if strings.Contains(logText, "run-shell 'exit") {
		t.Fatalf("ineligible attach path can print a synthetic shell failure:\n%s", logText)
	}
	if strings.Contains(logText, "set-environment -g") {
		t.Fatalf("attach rejection state leaks through tmux's inherited global environment:\n%s", logText)
	}
}

func TestAttachReusableTmuxSessionFallsBackWhenIdentityVanished(t *testing.T) {
	testDir := t.TempDir()
	tmuxPath := filepath.Join(testDir, "tmux")
	script := `#!/bin/sh
if [ "$1" = "if-shell" ] || [ "$1" = "has-session" ]; then
  exit 1
fi
exit 2
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	attached, err := attachReusableTmuxSession(tmuxPath, tmuxSessionReference{name: "everyapi-v3-codex-project-random", paneID: "%42"})
	if err != nil {
		t.Fatal(err)
	}
	if attached {
		t.Fatal("vanished tmux identity was treated as attached")
	}
}

func isolatedTmuxServer(t *testing.T) string {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	runtimeDirectory, err := os.MkdirTemp("/tmp", "everyapi-tmux-race-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", runtimeDirectory)
	t.Cleanup(func() {
		_ = exec.Command(tmuxPath, "kill-server").Run()
		_ = os.RemoveAll(runtimeDirectory)
	})
	return tmuxPath
}

func waitForTmuxFormat(t *testing.T, tmuxPath, target, format, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		output, err := exec.Command(tmuxPath, "display-message", "-p", "-t", target, format).CombinedOutput()
		if err == nil && strings.TrimSpace(string(output)) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("tmux format %q for %q did not become %q: err=%v output=%q", format, target, want, err, output)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func startDeadManagedTmuxSession(t *testing.T, tmuxPath, name string) string {
	t.Helper()
	gate := filepath.Join(os.Getenv("TMUX_TMPDIR"), "exit-gate")
	output, err := exec.Command(
		tmuxPath, "new-session", "-d", "-s", name,
		"sh", "-c", `while [ ! -e "$1" ]; do sleep 0.01; done`, "sh", gate, tmuxUseWrapperCommand,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("start managed tmux fixture: %v\n%s", err, output)
	}
	target := exactTmuxSessionTarget(name)
	paneOutput, err := exec.Command(tmuxPath, "list-panes", "-t", target, "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("find managed fixture pane: %v\n%s", err, paneOutput)
	}
	paneID := strings.TrimSpace(string(paneOutput))
	if output, err := exec.Command(tmuxPath, "set-option", "-w", "-t", paneID, "remain-on-exit", "on").CombinedOutput(); err != nil {
		t.Fatalf("enable remain-on-exit: %v\n%s", err, output)
	}
	if err := os.WriteFile(gate, []byte("exit"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForTmuxFormat(t, tmuxPath, paneID, "#{pane_dead}", "1")
	return paneID
}

func TestPruneDeadTmuxSessionRevalidatesRevivedPane(t *testing.T) {
	tmuxPath := isolatedTmuxServer(t)
	sessionName := "everyapi-v3-codex-0123456789ab-" + strings.Repeat("6", 32)
	managedPaneID := startDeadManagedTmuxSession(t, tmuxPath, sessionName)
	target := exactTmuxSessionTarget(sessionName)
	if output, err := exec.Command(tmuxPath, "respawn-pane", "-k", "-t", managedPaneID, "sleep", "30").CombinedOutput(); err != nil {
		t.Fatalf("revive pane before cleanup: %v\n%s", err, output)
	}
	waitForTmuxFormat(t, tmuxPath, managedPaneID, "#{pane_dead}", "0")

	pruneDeadTmuxSession(tmuxPath, tmuxSessionReference{name: sessionName, paneID: managedPaneID})
	if err := exec.Command(tmuxPath, "has-session", "-t", target).Run(); err != nil {
		t.Fatal("cleanup deleted a session whose pane revived after inventory")
	}
}

func TestPruneDeadTmuxSessionRemovesSoleDeadManagedPane(t *testing.T) {
	tmuxPath := isolatedTmuxServer(t)
	sessionName := "everyapi-v3-codex-0123456789ab-" + strings.Repeat("a", 32)
	managedPaneID := startDeadManagedTmuxSession(t, tmuxPath, sessionName)
	conditionOutput, err := exec.Command(tmuxPath, "display-message", "-p", "-t", managedPaneID, tmuxDeadCleanupCondition(sessionName)).CombinedOutput()
	if err != nil || strings.TrimSpace(string(conditionOutput)) != "1" {
		t.Fatalf("sole dead managed pane cleanup condition = %q, err=%v, want 1", conditionOutput, err)
	}

	pruneDeadTmuxSession(tmuxPath, tmuxSessionReference{name: sessionName, paneID: managedPaneID})
	if err := exec.Command(tmuxPath, "has-session", "-t", exactTmuxSessionTarget(sessionName)).Run(); err == nil {
		t.Fatal("cleanup preserved a standard managed session whose sole pane was dead")
	}
}

func TestPruneDeadTmuxSessionDoesNotFollowReusedServerID(t *testing.T) {
	tmuxPath := isolatedTmuxServer(t)
	staleName := "everyapi-v3-codex-0123456789ab-" + strings.Repeat("7", 32)
	stalePaneID := startDeadManagedTmuxSession(t, tmuxPath, staleName)
	if err := exec.Command(tmuxPath, "kill-server").Run(); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(tmuxPath, "new-session", "-d", "-s", "user-session", "sleep", "30").CombinedOutput(); err != nil {
		t.Fatalf("start replacement user session: %v\n%s", err, output)
	}

	pruneDeadTmuxSession(tmuxPath, tmuxSessionReference{name: staleName, paneID: stalePaneID})
	if err := exec.Command(tmuxPath, "has-session", "-t", "=user-session").Run(); err != nil {
		t.Fatal("cleanup followed a reused server-local ID and deleted the replacement user session")
	}
}

func TestAttachReusableTmuxSessionRevalidatesManagedPaneAndExactName(t *testing.T) {
	t.Run("managed pane died while user window survived", func(t *testing.T) {
		tmuxPath := isolatedTmuxServer(t)
		sessionName := "everyapi-v3-codex-0123456789ab-" + strings.Repeat("8", 32)
		managedPaneID := startDeadManagedTmuxSession(t, tmuxPath, sessionName)
		if output, err := exec.Command(tmuxPath, "new-window", "-d", "-t", exactTmuxSessionTarget(sessionName), "sleep", "30").CombinedOutput(); err != nil {
			t.Fatalf("add live user window: %v\n%s", err, output)
		}

		attached, err := attachReusableTmuxSession(tmuxPath, tmuxSessionReference{name: sessionName, paneID: managedPaneID})
		if err != nil {
			t.Fatal(err)
		}
		if attached {
			t.Fatal("session with a dead managed pane was reattached")
		}
		if err := exec.Command(tmuxPath, "has-session", "-t", exactTmuxSessionTarget(sessionName)).Run(); err != nil {
			t.Fatal("ineligible session with a live user window was not preserved")
		}
	})

	t.Run("managed pane was removed while user window survived", func(t *testing.T) {
		tmuxPath := isolatedTmuxServer(t)
		sessionName := "everyapi-v3-codex-0123456789ab-" + strings.Repeat("c", 32)
		if output, err := exec.Command(tmuxPath, "new-session", "-d", "-s", sessionName, "sh", "-c", "while :; do sleep 1; done", "sh", tmuxUseWrapperCommand).CombinedOutput(); err != nil {
			t.Fatalf("start managed session: %v\n%s", err, output)
		}
		paneOutput, err := exec.Command(tmuxPath, "list-panes", "-t", exactTmuxSessionTarget(sessionName), "-F", "#{pane_id}").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		managedPaneID := strings.TrimSpace(string(paneOutput))
		if output, err := exec.Command(tmuxPath, "new-window", "-d", "-t", exactTmuxSessionTarget(sessionName), "sleep", "30").CombinedOutput(); err != nil {
			t.Fatalf("add live user window: %v\n%s", err, output)
		}
		if output, err := exec.Command(tmuxPath, "kill-pane", "-t", managedPaneID).CombinedOutput(); err != nil {
			t.Fatalf("remove managed pane: %v\n%s", err, output)
		}

		attached, err := attachReusableTmuxSession(tmuxPath, tmuxSessionReference{name: sessionName, paneID: managedPaneID})
		if err != nil {
			t.Fatalf("removed managed pane should fall back without an attach error: %v", err)
		}
		if attached {
			t.Fatal("session whose managed pane was removed was reattached")
		}
		if err := exec.Command(tmuxPath, "has-session", "-t", exactTmuxSessionTarget(sessionName)).Run(); err != nil {
			t.Fatal("user window was not preserved")
		}
	})

	t.Run("server restarted and reused pane ID", func(t *testing.T) {
		tmuxPath := isolatedTmuxServer(t)
		staleName := "everyapi-v3-codex-0123456789ab-" + strings.Repeat("9", 32)
		if output, err := exec.Command(tmuxPath, "new-session", "-d", "-s", staleName, "sh", "-c", "while :; do sleep 1; done", "sh", tmuxUseWrapperCommand).CombinedOutput(); err != nil {
			t.Fatalf("start stale managed session: %v\n%s", err, output)
		}
		paneOutput, err := exec.Command(tmuxPath, "list-panes", "-t", exactTmuxSessionTarget(staleName), "-F", "#{pane_id}").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		stalePaneID := strings.TrimSpace(string(paneOutput))
		if err := exec.Command(tmuxPath, "kill-server").Run(); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(tmuxPath, "new-session", "-d", "-s", "replacement-user", "sleep", "30").CombinedOutput(); err != nil {
			t.Fatalf("start replacement user session: %v\n%s", err, output)
		}

		attached, err := attachReusableTmuxSession(tmuxPath, tmuxSessionReference{name: staleName, paneID: stalePaneID})
		if err != nil {
			t.Fatal(err)
		}
		if attached {
			t.Fatal("reattach followed a pane ID reused by a restarted tmux server")
		}
		if err := exec.Command(tmuxPath, "has-session", "-t", "=replacement-user").Run(); err != nil {
			t.Fatal("replacement user session was disturbed")
		}
	})
}

func TestTmuxEnvironmentFileRoundTripAndChildOverlay(t *testing.T) {
	statusDirectory, err := os.MkdirTemp("/tmp", "everyapi-tmux-test-")
	if err != nil {
		t.Fatal(err)
	}
	statusSocket := filepath.Join(statusDirectory, tmuxStatusSocketName)
	environmentFile := filepath.Join(statusDirectory, tmuxEnvironmentFileName)
	t.Cleanup(func() {
		_ = os.Remove(environmentFile)
		_ = os.Remove(statusSocket)
		_ = os.Remove(statusDirectory)
	})
	outerEnvironment := []string{"PATH=/outer/bin", "TERM=xterm-256color", "XDG_CONFIG_HOME=/tmp/config"}
	if err := writeTmuxEnvironment(environmentFile, outerEnvironment); err != nil {
		t.Fatal(err)
	}
	loaded, runtimeDirectory, err := loadTmuxEnvironment(environmentFile, statusSocket)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeDirectory != statusDirectory {
		t.Fatalf("runtime directory = %q, want %q", runtimeDirectory, statusDirectory)
	}
	if !reflect.DeepEqual(loaded, outerEnvironment) {
		t.Fatalf("loaded environment = %#v, want %#v", loaded, outerEnvironment)
	}
	if _, err := os.Stat(environmentFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("environment file remained after consumption: %v", err)
	}
	childEnvironment := tmuxChildEnvironment(loaded, []string{"TMUX=/tmp/tmux,1,0", "TMUX_PANE=%4", "TERM=tmux-256color", tools.TerminalModeEnvironment + "=tmux", tools.TmuxSessionEnvironment + "=everyapi-123-456", tools.TmuxAttachCommandEnvironment + "=tmux attach -t everyapi-123-456"}, "payload", statusSocket)
	wantValues := map[string]string{
		"PATH":                             "/outer/bin",
		"XDG_CONFIG_HOME":                  "/tmp/config",
		"TERM":                             "tmux-256color",
		"TMUX":                             "/tmp/tmux,1,0",
		"TMUX_PANE":                        "%4",
		tmuxUseArgsEnv:                     "payload",
		tmuxStatusSocketEnv:                statusSocket,
		tools.TerminalModeEnvironment:      "tmux",
		tools.TmuxSessionEnvironment:       "everyapi-123-456",
		tools.TmuxAttachCommandEnvironment: "tmux attach -t everyapi-123-456",
	}
	gotValues := make(map[string]string)
	for _, entry := range childEnvironment {
		parts := strings.SplitN(entry, "=", 2)
		gotValues[parts[0]] = parts[1]
	}
	for key, want := range wantValues {
		if gotValues[key] != want {
			t.Errorf("child environment %s = %q, want %q", key, gotValues[key], want)
		}
	}
	if _, exists := gotValues[tmuxEnvironmentFileEnv]; exists {
		t.Fatalf("%s leaked into the inner EveryAPI environment", tmuxEnvironmentFileEnv)
	}
}

func TestRunTmuxChildAndReportPreservesExitCode(t *testing.T) {
	statusDirectory, err := os.MkdirTemp("/tmp", "everyapi-tmux-test-")
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(statusDirectory, "status.sock")
	t.Cleanup(func() {
		_ = os.Remove(socketPath)
		_ = os.Remove(statusDirectory)
	})
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	reported := make(chan int, 1)
	acceptErr := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		defer connection.Close()
		data, err := io.ReadAll(io.LimitReader(connection, 16))
		if err != nil {
			acceptErr <- err
			return
		}
		code, err := strconv.Atoi(string(data))
		if err != nil {
			acceptErr <- err
			return
		}
		reported <- code
	}()

	code, err := runTmuxChildAndReport(exec.Command("sh", "-c", "exit 7"), socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("child exit code = %d, want 7", code)
	}
	select {
	case got := <-reported:
		if got != 7 {
			t.Fatalf("reported exit code = %d, want 7", got)
		}
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tmux child exit status")
	}
}

func TestCommandExitCodeMapsSignalsLikeAShell(t *testing.T) {
	err := exec.Command("sh", "-c", "kill -TERM $$").Run()
	if got := commandExitCode(err); got != 143 {
		t.Fatalf("SIGTERM exit code = %d, want 143", got)
	}
}

func TestRunTmuxChildAndReportSurvivesForegroundGroupInterrupt(t *testing.T) {
	const helperEnv = "EVERYAPI_TMUX_SIGNAL_HELPER"
	if os.Getenv(helperEnv) == "1" {
		child := exec.Command("sh", "-c", "trap 'exit 7' INT; echo ready; while :; do sleep 1; done")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		exitCode, err := runTmuxChildAndReport(child, os.Getenv(tmuxStatusSocketEnv))
		if err != nil {
			os.Exit(98)
		}
		os.Exit(exitCode)
	}

	statusDirectory, err := os.MkdirTemp("/tmp", "everyapi-tmux-signal-test-")
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(statusDirectory, "status.sock")
	t.Cleanup(func() {
		_ = os.Remove(socketPath)
		_ = os.Remove(statusDirectory)
	})
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	helper := exec.Command(os.Args[0], "-test.run=^TestRunTmuxChildAndReportSurvivesForegroundGroupInterrupt$")
	helper.Env = append(os.Environ(), helperEnv+"=1", tmuxStatusSocketEnv+"="+socketPath)
	helper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	helper.Stderr = os.Stderr
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && line != "ready\n" {
			err = fmt.Errorf("helper readiness line = %q", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	// A race-instrumented helper can take several seconds to start when all CLI
	// packages test concurrently. Wait for the actual readiness line with a
	// generous failure bound so scheduler load is not reported as product
	// signal-handling failure.
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for signal helper")
	}
	if err := syscall.Kill(-helper.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waitErr := helper.Wait()
	var exitError *exec.ExitError
	if !errors.As(waitErr, &exitError) || exitError.ExitCode() != 7 {
		t.Fatalf("helper wait error = %v, want exit code 7", waitErr)
	}
	if err := listener.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	connection, err := listener.Accept()
	if err != nil {
		t.Fatalf("wrapper did not report after SIGINT: %v", err)
	}
	defer connection.Close()
	data, err := io.ReadAll(io.LimitReader(connection, 16))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "7" {
		t.Fatalf("reported exit code = %q, want 7", data)
	}
}

func TestShouldRelaunchInTmuxDoesNotNest(t *testing.T) {
	if !shouldRelaunchInTmux("tmux", "") {
		t.Fatal("tmux mode outside tmux did not request a relaunch")
	}
	if shouldRelaunchInTmux("tmux", "/private/tmp/tmux-501/default,1,0") {
		t.Fatal("tmux mode inside tmux requested a nested session")
	}
	if shouldRelaunchInTmux("native", "") {
		t.Fatal("native mode requested a tmux relaunch")
	}
}

func TestApplyTerminalModeAdoptsExistingTmuxSession(t *testing.T) {
	binDir := t.TempDir()
	tmuxPath := filepath.Join(binDir, "tmux")
	script := "#!/bin/sh\nif [ \"$1\" = display-message ] && [ \"$2\" = -p ] && [ \"$3\" = '#S' ]; then\n  printf \"existing user's session\\n\"\n  exit 0\nfi\nexit 2\n"
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	if err := applyTerminalMode("tmux", []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(tools.TerminalModeEnvironment); got != "tmux" {
		t.Fatalf("%s = %q, want tmux", tools.TerminalModeEnvironment, got)
	}
	if got := os.Getenv(tools.TmuxSessionEnvironment); got != "existing user's session" {
		t.Fatalf("%s = %q", tools.TmuxSessionEnvironment, got)
	}
	if got := os.Getenv(tools.TmuxAttachCommandEnvironment); got != `tmux attach -t 'existing user'"'"'s session'` {
		t.Fatalf("%s = %q", tools.TmuxAttachCommandEnvironment, got)
	}
	if instructions := tools.TmuxAgentInstructions(); !strings.Contains(instructions, "existing user's session") {
		t.Fatalf("agent instructions = %q", instructions)
	}
}

func TestRelaunchUseInTmuxReportsMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := relaunchUseInTmux([]string{"claude"})
	if err == nil || !strings.Contains(err.Error(), "tmux is not installed") {
		t.Fatalf("error = %v, want missing-tmux guidance", err)
	}
}

// A clean exit is the user quitting their tool and needs no commentary. A
// non-zero one has to say something: the reattach hint printed at launch is
// stale by then, and the tool's own failure output died with the pane.
func TestTmuxSessionEndedNoticeSpeaksOnlyForFailures(t *testing.T) {
	const session = "everyapi-v3-codex-3256d52ef013-8704e3c7ee40fd964014c218388c7be6"
	if notice := tmuxSessionEndedNotice(session, 0); notice != "" {
		t.Fatalf("clean exit notice = %q, want silence", notice)
	}
	notice := tmuxSessionEndedNotice(session, 1)
	if !strings.Contains(notice, session) {
		t.Fatalf("notice = %q, want it to name session %q", notice, session)
	}
	if !strings.Contains(notice, "1") {
		t.Fatalf("notice = %q, want it to report the exit status", notice)
	}
	if strings.Contains(notice, "%!") {
		t.Fatalf("notice = %q, want every format argument consumed", notice)
	}
	// A signalled tool did not fail — somebody ended it, in a pane they were
	// looking at. commandExitCode renders that as 128+signo.
	for _, signalled := range []int{130, 143, 128 + 1, 128 + 31} {
		if notice := tmuxSessionEndedNotice(session, signalled); notice != "" {
			t.Errorf("exit %d notice = %q, want silence for a signalled tool", signalled, notice)
		}
	}
	// A real failure code just above the signal range still gets the hint.
	if notice := tmuxSessionEndedNotice(session, 128+32); notice == "" {
		t.Error("exit 160 produced no notice, want the failure hint")
	}
}

// The recorded path is honoured only in the shape validateTmuxRuntimePaths
// demands of the socket and the environment file. A tampered environment must
// not be able to aim the write at an arbitrary file.
func TestRecordTmuxFatalErrorRefusesPathsOutsideItsRuntimeDirectory(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "everyapi-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })

	accepted := filepath.Join(directory, tmuxErrorFileName)
	refused := []string{
		"",
		filepath.Join(directory, "somewhere-else.txt"),    // wrong name
		filepath.Join("/tmp/not-ours", tmuxErrorFileName), // wrong directory prefix
		filepath.Join("/tmp", tmuxErrorFileName),          // no per-launch directory
		filepath.Join(os.TempDir(), "sub", "dir", "error.txt"),
	}
	for _, path := range refused {
		if validTmuxErrorFilePath(path) {
			t.Errorf("path %q accepted, want refusal", path)
		}
	}
	if !validTmuxErrorFilePath(accepted) {
		t.Fatalf("path %q refused, want acceptance", accepted)
	}

	// End to end through the package var, the way main.go reaches it.
	previous := tmuxErrorFile
	t.Cleanup(func() { tmuxErrorFile = previous })
	tmuxErrorFile = accepted
	RecordTmuxFatalError("not logged in — run 'everyapi auth login'")
	if got := readTmuxFatalError(accepted); got != "not logged in — run 'everyapi auth login'" {
		t.Fatalf("read back %q", got)
	}

	// A refused path must leave nothing behind.
	tmuxErrorFile = filepath.Join(directory, "somewhere-else.txt")
	RecordTmuxFatalError("should not be written")
	if _, err := os.Stat(tmuxErrorFile); err == nil {
		t.Fatal("a refused path was written anyway")
	}
}

// The relayed text reaches a real terminal, so escape sequences in it — a
// backend-relayed message is untrusted — must be neutralized, and the size
// bounded.
func TestReadTmuxFatalErrorSanitizesAndBounds(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "everyapi-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, tmuxErrorFileName)

	if err := os.WriteFile(path, []byte("\x1b[2Jcleared your screen\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readTmuxFatalError(path)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("read back %q, want the escape neutralized", got)
	}
	if !strings.Contains(got, "cleared your screen") {
		t.Fatalf("read back %q, want the message text kept", got)
	}

	if err := os.WriteFile(path, []byte(strings.Repeat("x", tmuxFatalErrorLimit*2)), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := len(readTmuxFatalError(path)); n > tmuxFatalErrorLimit {
		t.Fatalf("read back %d bytes, want at most %d", n, tmuxFatalErrorLimit)
	}

	// Nothing recorded, and a path that does not exist, both mean "no message" —
	// the generic session-ended notice covers those.
	if got := readTmuxFatalError(filepath.Join(directory, "missing.txt")); got != "" {
		t.Fatalf("missing file returned %q", got)
	}
	if got := readTmuxFatalError(""); got != "" {
		t.Fatalf("empty path returned %q", got)
	}
}

// The wrapper must hand the recorded path down to the process that actually
// raises the error, alongside the other pane-scoped variables.
func TestTmuxChildEnvironmentCarriesTheErrorFile(t *testing.T) {
	child := tmuxChildEnvironment(
		[]string{"HOME=/Users/e"},
		[]string{"TERM=xterm", tmuxErrorFileEnv + "=/tmp/everyapi-tmux-abc/error.txt"},
		"payload",
		"/tmp/everyapi-tmux-abc/status.sock",
	)
	value, ok := environmentValue(child, tmuxErrorFileEnv)
	if !ok || value != "/tmp/everyapi-tmux-abc/error.txt" {
		t.Fatalf("child env %s = %q (present=%v), want the wrapper's value", tmuxErrorFileEnv, value, ok)
	}
}

// Adopting a live session silently would drop the user into an existing
// conversation while they thought they were launching a tool. Only an explicit
// resume may do that; everything else asks, and a script — which has nobody to
// ask — keeps the old behaviour of always starting fresh.
func TestTmuxSessionToAdoptAsksBeforeSteppingIntoALiveSession(t *testing.T) {
	candidate := tmuxSessionReference{name: "everyapi-v3-codex-3256d52ef013-0000000000000000000000000000000a", paneID: "%1"}
	refuse := func(tmuxSessionReference) (bool, error) { return false, nil }
	accept := func(tmuxSessionReference) (bool, error) { return true, nil }
	mustNotAsk := func(tmuxSessionReference) (bool, error) {
		t.Fatal("a launch asked when it should not have")
		return false, nil
	}

	tests := []struct {
		name        string
		candidate   tmuxSessionReference
		autoAdopt   bool
		interactive bool
		ask         func(tmuxSessionReference) (bool, error)
		want        string
	}{
		{name: "nothing to adopt", ask: mustNotAsk},
		{name: "explicit resume takes it silently", candidate: candidate, autoAdopt: true, interactive: true, ask: mustNotAsk, want: candidate.name},
		// A resume from a script still adopts: the argv already said what to do.
		{name: "explicit resume from a script", candidate: candidate, autoAdopt: true, ask: mustNotAsk, want: candidate.name},
		{name: "script never adopts otherwise", candidate: candidate, ask: mustNotAsk},
		{name: "terminal asks and may accept", candidate: candidate, interactive: true, ask: accept, want: candidate.name},
		{name: "terminal asks and may refuse", candidate: candidate, interactive: true, ask: refuse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := tmuxSessionToAdopt(test.candidate, test.autoAdopt, test.interactive, test.ask)
			if err != nil {
				t.Fatal(err)
			}
			if got.name != test.want {
				t.Fatalf("adopted %q, want %q", got.name, test.want)
			}
		})
	}
}

// Cancelling the prompt (Esc) launches nothing, the same thing Esc does at every
// other picker in the CLI.
func TestTmuxSessionToAdoptPropagatesACancelledPrompt(t *testing.T) {
	candidate := tmuxSessionReference{name: "everyapi-v3-codex-3256d52ef013-0000000000000000000000000000000a"}
	want := errors.New("cancelled")
	_, err := tmuxSessionToAdopt(candidate, false, true, func(tmuxSessionReference) (bool, error) {
		return false, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// The idle figure only decorates the prompt, so a tmux that cannot answer must
// degrade rather than fail the launch.
func TestTmuxSessionIdleLabelDegradesWhenTmuxCannotAnswer(t *testing.T) {
	got := tmuxSessionIdleLabel("/nonexistent/tmux", "everyapi-v3-codex-3256d52ef013-0000000000000000000000000000000a")
	if got != i18n.T("use.tmux_adopt_idle_unknown") {
		t.Fatalf("idle label = %q, want the unknown marker", got)
	}
}
