package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func writeClaudeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectClaudeSessionPollutionBacktracksToFirstHint(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"user","uuid":"u1","timestamp":"2026-07-13T02:00:00Z","message":{"role":"user","content":"Audit every interactive screen and fix real bugs."}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","timestamp":"2026-07-13T02:01:00Z","message":{"id":"msg_clean","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"The public pages are clean. Next I will inspect admin routes."}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","timestamp":"2026-07-13T02:01:01Z","message":{"id":"msg_clean","role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool_1","name":"Bash","input":{}}]}}`,
		`{"type":"assistant","uuid":"b1","parentUuid":"r1","timestamp":"2026-07-13T02:57:26Z","message":{"id":"msg_bad_1","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"The title mapping is incomplete.\n\n课件我需要看几张交互态，再统一修复。"}]}}`,
		`{"type":"assistant","uuid":"b2","parentUuid":"b1","timestamp":"2026-07-13T02:57:28Z","message":{"id":"msg_bad_1","role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool_2","name":"TaskCreate","input":{}}]}}`,
		`{"type":"assistant","uuid":"b3","parentUuid":"r2","timestamp":"2026-07-13T02:58:23Z","message":{"id":"msg_bad_2","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"课件看这些交互态。\n\n課\n\n课件先看主题切换。"}]}}`,
		`{"type":"assistant","uuid":"b4","parentUuid":"r3","timestamp":"2026-07-13T02:59:10Z","message":{"id":"msg_bad_3","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"course\n\ncourse\n\ncourt"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected pollution detection")
	}
	if got.FirstMessageID != "msg_bad_1" || got.FirstLine != 4 {
		t.Fatalf("first pollution = (%q, line %d), want (msg_bad_1, line 4)", got.FirstMessageID, got.FirstLine)
	}
	if got.FirstTimestamp != "2026-07-13T02:57:26Z" {
		t.Fatalf("first timestamp = %q", got.FirstTimestamp)
	}
	if got.AffectedMessages != 3 {
		t.Fatalf("affected messages = %d, want 3", got.AffectedMessages)
	}
}

func TestDetectClaudeSessionPollutionCatchesMangledToolXML(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"Run the check."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_mangled","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"call:\n\nantml:invoke name=\"EnterWorktree\">\n<parameter name=\"path\">/tmp/wt</parameter>"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.FirstMessageID != "msg_mangled" {
		t.Fatalf("mangled leaked tool XML not detected: %#v", got)
	}
}

func TestDetectClaudeSessionPollutionClusterBoundaryCoversEarliestMember(t *testing.T) {
	// The cluster confirms only via msg_first_bad (2 tokens) + msg_last_bad
	// (1 token), separated by two clean groups. The boundary must land on
	// msg_first_bad, not the trigger, or the polluted member stays in the
	// "clean" clone.
	path := writeClaudeTranscript(t,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"Continue the task."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_first_bad","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"call\ncourse"}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:20Z","message":{"id":"msg_clean_1","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Normal clean context A."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:40Z","message":{"id":"msg_clean_2","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Normal clean context B."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:01:00Z","message":{"id":"msg_last_bad","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"count"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected pollution detection")
	}
	if got.FirstMessageID != "msg_first_bad" || got.FirstLine != 2 {
		t.Fatalf("cluster boundary = (%q, line %d), want (msg_first_bad, line 2)", got.FirstMessageID, got.FirstLine)
	}
}

func TestDetectClaudeSessionPollutionMarksSelfRecoveredSessions(t *testing.T) {
	lines := []string{
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"Continue the task."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_bad","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Checking now:\n<invoke name=\"Bash\">\n<parameter name=\"command\">go test ./...</parameter>\n</invoke>"}]}}`,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"Looks broken, keep going."}}`,
	}
	for i := 0; i < 4; i++ {
		lines = append(lines,
			`{"type":"assistant","timestamp":"2026-07-13T03:1`+strconv.Itoa(i)+`:00Z","message":{"id":"msg_clean_`+strconv.Itoa(i)+`","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Substantial clean follow-up work."}]}}`,
		)
	}
	got, err := detectClaudeSessionPollution(writeClaudeTranscript(t, lines...))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected pollution detection")
	}
	if !got.SelfRecovered {
		t.Fatalf("session with a large clean tail under a later user turn was not marked self-recovered: %#v", got)
	}

	// The same burst with no clean tail must NOT be marked self-recovered.
	got2, err := detectClaudeSessionPollution(writeClaudeTranscript(t, lines[:2]...))
	if err != nil {
		t.Fatal(err)
	}
	if got2 == nil || got2.SelfRecovered {
		t.Fatalf("tail-less pollution misclassified: %#v", got2)
	}
}

func TestDetectClaudeSessionPollutionCatchesBareToolXML(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"Run the check."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_xml","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Checking now:\n<invoke name=\"Bash\">\n<parameter name=\"command\">go test ./...</parameter>\n</invoke>"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.FirstMessageID != "msg_xml" {
		t.Fatalf("detection = %#v, want msg_xml", got)
	}
}

func TestDetectClaudeSessionPollutionCatchesTerminalParseFailure(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_parse","role":"assistant","stop_reason":"stop_sequence","content":[{"type":"text","text":"The model's tool call could not be parsed (retry also failed)."}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.FirstMessageID != "msg_parse" {
		t.Fatalf("detection = %#v, want msg_parse", got)
	}
}

func TestDetectClaudeSessionPollutionIgnoresQuotedAndLegitimateText(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"assistant","message":{"id":"msg_legit","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"这个课件需要增加一页课程简介。"}]}}`,
		`{"type":"assistant","message":{"id":"msg_quoted","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"A broken call can look like:\n`+"```xml"+`\ncourt\n<invoke name=\"Bash\">\n<parameter name=\"command\">ls</parameter>\n</invoke>\n`+"```"+`\nUse `+"`course`"+` only as an example."}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("false positive: %#v", got)
	}
}

func TestDetectClaudeSessionPollutionDoesNotClusterAcrossUserTurns(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"Translate the first word."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_1","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"call"}]}}`,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"Translate the second word."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:30Z","message":{"id":"msg_2","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"course"}]}}`,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"Translate the third word."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:01:00Z","message":{"id":"msg_3","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"count"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("standalone words across user turns caused a false positive: %#v", got)
	}
}

func TestDetectClaudeSessionPollutionIgnoresStandaloneWordsWithoutToolContext(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"List the related English words."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_words","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"call\ncourse\ncourt\ncount"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("standalone words in a normal answer caused a false positive: %#v", got)
	}
}

func TestDetectClaudeSessionPollutionClustersWithinOneUserTurn(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"Continue the task."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_1","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"call"}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:30Z","message":{"id":"msg_2","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"course"}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:01:00Z","message":{"id":"msg_3","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"count"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.FirstMessageID != "msg_1" {
		t.Fatalf("same-turn corruption cluster not detected: %#v", got)
	}
}

func TestDetectClaudeSessionPollutionFailsOpenForMalformedJSONL(t *testing.T) {
	tests := map[string][]string{
		"malformed middle record": {
			`{"type":"user","message":{"role":"user","content":"Continue."}}`,
			`{"type":"assistant"`,
			`{"type":"assistant","message":{"id":"msg_bad","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"call\ncourse\ncount"}]}}`,
		},
		"malformed message content": {
			`{"type":"user","message":{"role":"user","content":{"unexpected":true}}}`,
			`{"type":"assistant","message":{"id":"msg_bad","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"call\ncourse\ncount"}]}}`,
		},
	}
	for name, lines := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := detectClaudeSessionPollution(writeClaudeTranscript(t, lines...))
			if err == nil || got != nil {
				t.Fatalf("detect malformed JSONL = (%#v, %v), want fail-open error", got, err)
			}
		})
	}
}

func TestDetectClaudeSessionPollutionDoesNotBacktrackAcrossWeakBoundary(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"Continue the task."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_weak","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"course"}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:10Z","message":{"id":"msg_clean_1","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Normal clean context A."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:20Z","message":{"id":"msg_clean_2","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Normal clean context B."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:30Z","message":{"id":"msg_bad","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"call\ncourt\ncount"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected pollution detection")
	}
	if got.FirstMessageID != "msg_bad" {
		t.Fatalf("pollution backtracked across weak boundary: %#v", got)
	}
}

func TestDetectClaudeSessionPollutionDoesNotBacktrackAcrossUserTurns(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"Translate one word."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_legit","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"course"}]}}`,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"NEW TASK"}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:30Z","message":{"id":"msg_bad","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"call\ncourse\ncount"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected pollution detection")
	}
	if got.FirstMessageID != "msg_bad" {
		t.Fatalf("pollution crossed a user turn: %#v", got)
	}
}

func TestDetectClaudeSessionPollutionDoesNotBacktrackAcrossLongGap(t *testing.T) {
	path := writeClaudeTranscript(t,
		`{"type":"user","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":"Long-running task."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T03:00:00Z","message":{"id":"msg_legit","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"course"}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T04:00:00Z","message":{"id":"msg_bad","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"call\ncourse\ncount"}]}}`,
	)

	got, err := detectClaudeSessionPollution(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.FirstMessageID != "msg_bad" {
		t.Fatalf("pollution backtracked across a long gap: %#v", got)
	}
}

func writeResumableClaudeSession(t *testing.T, claudeDir, sessionID string, polluted bool) string {
	t.Helper()
	project := filepath.Join(claudeDir, "projects", "-tmp-project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","timestamp":"2026-07-13T01:00:00Z","message":{"role":"user","content":"Review the checkout flow and fix confirmed bugs."}}`,
		`{"type":"assistant","timestamp":"2026-07-13T01:01:00Z","message":{"id":"msg_clean","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Checkout is verified. I am now checking admin navigation."},{"type":"tool_use","id":"tool_clean","name":"Read","input":{"file_path":"status.txt"}}]}}`,
		`{"type":"user","timestamp":"2026-07-13T01:01:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_clean","content":"FULL CLEAN TOOL RESULT"}]}}`,
		`{"type":"file-history-snapshot","timestamp":"2026-07-13T01:01:02Z","snapshot":{"trackedFileBackups":{"status.txt":{"backupFileName":"backup-1","version":1}}}}`,
	}
	if polluted {
		lines = append(lines,
			`{"type":"assistant","timestamp":"2026-07-13T02:57:26Z","message":{"id":"msg_bad_1","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"课件我需要继续检查。"}]}}`,
			`{"type":"assistant","timestamp":"2026-07-13T02:58:23Z","message":{"id":"msg_bad_2","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"课件继续。\n\n課\n\n课件执行。"}]}}`,
			`{"type":"assistant","timestamp":"2026-07-13T02:59:10Z","message":{"id":"msg_bad_3","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"course\n\ncourt\n\ncount"}]}}`,
		)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := range lines {
		lines[i] = strings.Replace(
			lines[i],
			`{"type":`,
			`{"sessionId":`+strconv.Quote(sessionID)+`,"cwd":`+strconv.Quote(cwd)+`,"type":`,
			1,
		)
	}
	path := filepath.Join(project, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecoverClaudeResumeCreatesCompleteCleanClone(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--dangerously-skip-permissions", "--resume", oldID, "--fork-session"}

	gotArgs, recovery, err := recoverClaudeResume(args, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil {
		t.Fatal("expected a recovery")
	}
	if recovery.OriginalSessionID != oldID || recovery.NewSessionID != newID {
		t.Fatalf("recovery IDs = (%q, %q)", recovery.OriginalSessionID, recovery.NewSessionID)
	}
	wantArgs := []string{"--dangerously-skip-permissions", "--resume", newID}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("recovered args = %#v, want %#v", gotArgs, wantArgs)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, original) {
		t.Fatal("original transcript was modified")
	}
	clonePath := filepath.Join(filepath.Dir(path), newID+".jsonl")
	clone, err := os.ReadFile(clonePath)
	if err != nil {
		t.Fatal(err)
	}
	cloneText := string(clone)
	for _, wanted := range []string{
		newID,
		"Review the checkout flow",
		"Checkout is verified",
		"FULL CLEAN TOOL RESULT",
		"file-history-snapshot",
	} {
		if !strings.Contains(cloneText, wanted) {
			t.Fatalf("clean clone missing %q", wanted)
		}
	}
	for _, unwanted := range []string{oldID, "课件", "course", "court", "count"} {
		if strings.Contains(cloneText, unwanted) {
			t.Fatalf("clean clone contains %q", unwanted)
		}
	}
	info, err := os.Stat(clonePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("clean clone mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRecoverClaudeResumePreservesCompleteLongCleanContent(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	longState := "LONG-CLEAN-STATE:" + strings.Repeat("complete-context-", 2_000)
	data = []byte(strings.Replace(
		string(data),
		"Checkout is verified. I am now checking admin navigation.",
		longState,
		1,
	))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, recovery, err := recoverClaudeResume([]string{"--resume", oldID}, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil {
		t.Fatal("expected a recovery")
	}
	clone, err := os.ReadFile(filepath.Join(filepath.Dir(path), newID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clone), longState) {
		t.Fatalf("clean clone truncated %d-byte context", len(longState))
	}
}

func TestRecoverClaudeResumeCarriesCompletedGoalStatePastPollutionBoundary(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	condition := "capture every interactive page"
	original := injectClaudeGoalState(t, path, oldID, condition, true)

	_, recovery, err := recoverClaudeResume([]string{"--resume", oldID}, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil {
		t.Fatal("expected a recovery")
	}
	clone, err := os.ReadFile(filepath.Join(filepath.Dir(path), newID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(clone, []byte(`"uuid":"goal-active"`)) ||
		!bytes.Contains(clone, []byte(`"uuid":"goal-complete"`)) ||
		!bytes.Contains(clone, []byte(`"met":true`)) {
		t.Fatalf("completed session goal state was not carried into recovery: %s", clone)
	}
	if bytes.Contains(clone, []byte("msg_bad")) || bytes.Contains(clone, []byte("course")) {
		t.Fatalf("polluted assistant content crossed the recovery boundary: %s", clone)
	}
	if !bytes.Contains(clone, []byte(`"parentUuid":"goal-active"`)) {
		t.Fatalf("carried goal completion was not reparented to the clean prefix: %s", clone)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("original transcript was modified")
	}
}

func TestRecoverClaudeResumeFindsGoalCompletionInProvenanceSession(t *testing.T) {
	root := t.TempDir()
	provenanceID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	failedRecoveryID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	newID := "11111111-2222-4333-8444-555555555555"
	condition := "capture every interactive page"
	provenancePath := writeResumableClaudeSession(t, root, provenanceID, true)
	injectClaudeGoalState(t, provenancePath, provenanceID, condition, true)
	failedPath := writeResumableClaudeSession(t, root, failedRecoveryID, true)
	injectClaudeGoalState(t, failedPath, failedRecoveryID, condition, false)

	_, recovery, err := recoverClaudeResume([]string{"--resume", failedRecoveryID}, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil {
		t.Fatal("expected a recovery")
	}
	clone, err := os.ReadFile(filepath.Join(filepath.Dir(failedPath), newID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(clone, []byte(`"uuid":"goal-complete"`)) ||
		!bytes.Contains(clone, []byte(`"met":true`)) {
		t.Fatalf("provenance goal completion was not carried into second-generation recovery: %s", clone)
	}
	if bytes.Contains(clone, []byte(provenanceID)) || bytes.Contains(clone, []byte(failedRecoveryID)) {
		t.Fatalf("recovery clone retained an old top-level session ID: %s", clone)
	}
}

func injectClaudeGoalState(t *testing.T, path, sessionID, condition string, completed bool) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	active := `{"sessionId":` + strconv.Quote(sessionID) + `,"cwd":` + strconv.Quote(mustGetwd(t)) + `,"parentUuid":"clean-parent","type":"attachment","uuid":"goal-active","attachment":{"type":"goal_status","met":false,"sentinel":true,"condition":` + strconv.Quote(condition) + `}}`
	data = []byte(strings.Replace(
		string(data),
		`{"sessionId":`+strconv.Quote(sessionID)+`,"cwd":`+strconv.Quote(mustGetwd(t))+`,"type":"assistant","timestamp":"2026-07-13T02:57:26Z"`,
		active+"\n"+`{"sessionId":`+strconv.Quote(sessionID)+`,"cwd":`+strconv.Quote(mustGetwd(t))+`,"type":"assistant","timestamp":"2026-07-13T02:57:26Z"`,
		1,
	))
	if completed {
		completion := `{"sessionId":` + strconv.Quote(sessionID) + `,"cwd":` + strconv.Quote(mustGetwd(t)) + `,"parentUuid":"polluted-parent","type":"attachment","uuid":"goal-complete","attachment":{"type":"goal_status","met":true,"condition":` + strconv.Quote(condition) + `,"reason":"completed after the malformed model turn"}}`
		data = append(data, []byte(completion+"\n")...)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}

func TestRecoverClaudeResumePreservesSessionIDMentionInsideCleanContent(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cleanText := oldID
	data = []byte(strings.Replace(
		string(data),
		"Review the checkout flow and fix confirmed bugs.",
		cleanText,
		1,
	))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, recovery, err := recoverClaudeResume([]string{"--resume", oldID}, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil {
		t.Fatal("expected a recovery")
	}
	clone, err := os.ReadFile(filepath.Join(filepath.Dir(path), newID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clone), cleanText) {
		t.Fatalf("session ID mention inside clean content was changed: %s", clone)
	}
}

func TestRecoverClaudeResumePreservesBlankLinesInCleanPrefix(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "\n", "\n\n", 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, recovery, err := recoverClaudeResume([]string{"--resume", oldID}, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil {
		t.Fatal("expected a recovery")
	}
	clone, err := os.ReadFile(filepath.Join(filepath.Dir(path), newID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(clone, []byte("\n\n")) {
		t.Fatalf("clean prefix blank line was not preserved: %q", clone)
	}
}

func TestPrepareClaudeSessionRecoveryFailsOpenWhenCleanCloneAlreadyExists(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	clonePath := filepath.Join(filepath.Dir(path), newID+".jsonl")
	const sentinel = "do not overwrite"
	if err := os.WriteFile(clonePath, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--resume", oldID}

	gotArgs, recovery := prepareClaudeSessionRecovery(
		args,
		root,
		func() (string, error) { return newID, nil },
	)
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("clone collision did not fail open: args=%v recovery=%#v", gotArgs, recovery)
	}
	got, err := os.ReadFile(clonePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("existing clone was overwritten: %q", got)
	}
}

func TestPublishClaudeCleanSessionDoesNotReplaceExistingTarget(t *testing.T) {
	root := t.TempDir()
	tempPath := filepath.Join(root, "complete.tmp")
	targetPath := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(tempPath, []byte("complete clean clone"), 0o600); err != nil {
		t.Fatal(err)
	}
	const sentinel = "existing session"
	if err := os.WriteFile(targetPath, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := publishClaudeCleanSession(tempPath, targetPath); err == nil {
		t.Fatal("publish replaced an existing target")
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("existing target changed to %q", got)
	}
}

func TestRecoverClaudeResumeSupportsEqualsAndExistingPrompt(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	writeResumableClaudeSession(t, root, oldID, true)
	args := []string{"--model", "opus", "--resume=" + oldID, "Focus on the failing tests."}

	gotArgs, recovery, err := recoverClaudeResume(args, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil {
		t.Fatal("expected a recovery")
	}
	wantArgs := []string{"--model", "opus", "--resume=" + newID, "Focus on the failing tests."}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("recovered args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestRecoverClaudeResumeLeavesHealthyOrUnresolvedSessionsAlone(t *testing.T) {
	root := t.TempDir()
	healthyID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	writeResumableClaudeSession(t, root, healthyID, false)
	tests := [][]string{
		{"--resume", healthyID},
		{"--resume", "ffffffff-ffff-4fff-8fff-ffffffffffff"},
		{"--resume"},
		{"--continue"},
	}
	for _, args := range tests {
		gotArgs, recovery, err := recoverClaudeResume(args, root, "11111111-2222-4333-8444-555555555555")
		if err != nil {
			t.Fatalf("recoverClaudeResume(%v): %v", args, err)
		}
		if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
			t.Fatalf("recoverClaudeResume(%v) = (%v, %#v), want unchanged", args, gotArgs, recovery)
		}
	}
}

func TestRecoverClaudeResumeFailsOpenWhenSessionCWDIsMissing(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(string(data), `"cwd":`+strconv.Quote(cwd)+`,`, ""))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--resume", oldID}

	gotArgs, recovery, err := recoverClaudeResume(args, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("session without cwd was cloned: args=%v recovery=%#v", gotArgs, recovery)
	}
}

func TestRecoverClaudeResumeFailsOpenForDuplicateSessionFiles(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	secondProject := filepath.Join(root, "projects", "-second-project")
	if err := os.MkdirAll(secondProject, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondProject, oldID+".jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--resume", oldID}

	gotArgs, recovery, err := recoverClaudeResume(args, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("duplicate session files were cloned: args=%v recovery=%#v", gotArgs, recovery)
	}
}

func TestPrepareClaudeSessionRecoveryFailsOpenForMixedSessionIDs(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	otherID := "99999999-aaaa-4bbb-8ccc-dddddddddddd"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(
		string(data),
		`"sessionId":`+strconv.Quote(oldID),
		`"sessionId":`+strconv.Quote(otherID),
		1,
	))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--resume", oldID}

	gotArgs, recovery := prepareClaudeSessionRecovery(
		args,
		root,
		func() (string, error) { return newID, nil },
	)
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("mixed session IDs did not fail open: args=%v recovery=%#v", gotArgs, recovery)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), newID+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("mixed session IDs left a clean clone: %v", err)
	}
}

func TestRecoverClaudeResumePreservesVariadicClaudeArgs(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	writeResumableClaudeSession(t, root, oldID, true)
	args := []string{"--resume", oldID, "--mcp-config", "one.json", "two.json"}

	gotArgs, recovery, err := recoverClaudeResume(args, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"--resume", newID, "--mcp-config", "one.json", "two.json"}
	if recovery == nil || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("variadic args were not preserved: got (%v, %#v)", gotArgs, recovery)
	}
}

func TestRecoverClaudeResumeFailsOpenForDuplicateResumeFlags(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	writeResumableClaudeSession(t, root, oldID, true)
	args := []string{"--resume=" + oldID, "--resume=" + oldID}

	gotArgs, recovery, err := recoverClaudeResume(args, root, "11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("duplicate resume flags rewritten: got (%v, %#v)", gotArgs, recovery)
	}
}

func TestRecoverClaudeResumeFailsOpenWhenCombinedWithContinue(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	writeResumableClaudeSession(t, root, oldID, true)
	args := []string{"--continue", "--resume", oldID}

	gotArgs, recovery, err := recoverClaudeResume(args, root, "11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("resume combined with continue was rewritten: got (%v, %#v)", gotArgs, recovery)
	}
}

func TestRecoverClaudeResumePreservesInnerEndOfOptions(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	writeResumableClaudeSession(t, root, oldID, true)
	args := []string{"--resume", oldID, "--", "-literal prompt"}

	gotArgs, recovery, err := recoverClaudeResume(args, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"--resume", newID, "--", "-literal prompt"}
	if recovery == nil || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("end-of-options argv was not preserved: got (%v, %#v)", gotArgs, recovery)
	}
}

func TestRecoverClaudeResumeRejectsNonUUIDSessionID(t *testing.T) {
	root := t.TempDir()
	writeResumableClaudeSession(t, root, "polluted", true)
	args := []string{"--resume=*"}

	gotArgs, recovery, err := recoverClaudeResume(args, root, "11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("non-UUID resume rewritten: got (%v, %#v)", gotArgs, recovery)
	}
}

func TestRecoverClaudeResumeFailsOpenForDifferentWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(string(data), `"type":"user"`, `"cwd":"/original/project","type":"user"`))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--resume", oldID}

	gotArgs, recovery, err := recoverClaudeResumeFromDir(args, root, "/different/project", newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("cross-directory resume rewritten: got (%v, %#v)", gotArgs, recovery)
	}
}

func TestRecoverClaudeResumeUsesInitialWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"type":"user"`, `"cwd":"/original/project","type":"user"`, 1))
	data = []byte(strings.Replace(string(data), `"type":"assistant"`, `"cwd":"/original/project/apps/frontend","type":"assistant"`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	gotArgs, recovery, err := recoverClaudeResumeFromDir(
		[]string{"--resume", oldID},
		root,
		"/original/project",
		newID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil || !strings.Contains(strings.Join(gotArgs, "\n"), "--resume\n"+newID) {
		t.Fatalf("same-project resume was not recovered: args=%v recovery=%#v", gotArgs, recovery)
	}
}

func TestRecoverClaudeResumeReusesExistingRecoveryClone(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	firstNewID := "11111111-2222-4333-8444-555555555555"
	secondNewID := "99999999-aaaa-4bbb-8ccc-dddddddddddd"
	path := writeResumableClaudeSession(t, root, oldID, true)

	_, first, err := recoverClaudeResume([]string{"--resume", oldID}, root, firstNewID)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || !first.CreatedClone || first.NewSessionID != firstNewID {
		t.Fatalf("first recovery = %#v", first)
	}

	gotArgs, second, err := recoverClaudeResume([]string{"--resume", oldID}, root, secondNewID)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil || !second.Reused || second.NewSessionID != firstNewID {
		t.Fatalf("second launch did not reuse the first clone: %#v", second)
	}
	if second.CreatedClone {
		t.Fatal("reuse must not mint another clone")
	}
	if !reflect.DeepEqual(gotArgs, []string{"--resume", firstNewID}) {
		t.Fatalf("reused args = %#v", gotArgs)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), secondNewID+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("second launch created a duplicate clone: %v", err)
	}
}

func TestClaudeSessionRecoveryDiscardRemovesAbandonedClone(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)

	_, recovery, err := recoverClaudeResume([]string{"--resume", oldID}, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil || !recovery.CreatedClone {
		t.Fatalf("recovery = %#v", recovery)
	}
	// A reused recovery must never delete the clone it points at.
	_, reused, err := recoverClaudeResume([]string{"--resume", oldID}, root, "99999999-aaaa-4bbb-8ccc-dddddddddddd")
	if err != nil {
		t.Fatal(err)
	}
	if reused == nil || !reused.Reused {
		t.Fatalf("expected reuse before discard: %#v", reused)
	}
	reused.discard()
	if _, err := os.Stat(recovery.CleanPath); err != nil {
		t.Fatalf("discard of a reused recovery removed the clone: %v", err)
	}

	recovery.discard()
	if _, err := os.Stat(recovery.CleanPath); !os.IsNotExist(err) {
		t.Fatalf("abandoned clone was not removed: %v", err)
	}
	if _, err := os.Stat(recovery.MarkerPath); !os.IsNotExist(err) {
		t.Fatalf("abandoned recovery marker was not removed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("original transcript touched by discard: %v", err)
	}
}

func TestRecoverClaudeResumeGuardOnlyForSelfRecoveredSessions(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	path := writeResumableClaudeSession(t, root, oldID, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cwd := mustGetwd(t)
	tail := []string{
		`{"sessionId":` + strconv.Quote(oldID) + `,"cwd":` + strconv.Quote(cwd) + `,"type":"user","origin":{"kind":"human"},"promptSource":"typed","timestamp":"2026-07-13T03:05:00Z","message":{"role":"user","content":"Keep going with the fix."}}`,
	}
	for i := 0; i < 4; i++ {
		tail = append(tail,
			`{"sessionId":`+strconv.Quote(oldID)+`,"cwd":`+strconv.Quote(cwd)+`,"type":"assistant","timestamp":"2026-07-13T03:1`+strconv.Itoa(i)+`:00Z","message":{"id":"msg_tail_`+strconv.Itoa(i)+`","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Clean post-recovery work."}]}}`,
		)
	}
	data = append(data, []byte(strings.Join(tail, "\n")+"\n")...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	args := []string{"--resume", oldID}
	gotArgs, recovery, err := recoverClaudeResume(args, root, newID)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil || !recovery.GuardOnly {
		t.Fatalf("self-recovered session was not guard-only: %#v", recovery)
	}
	if !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("guard-only resume must keep args unchanged: %#v", gotArgs)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), newID+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("guard-only resume minted a clone: %v", err)
	}
}

func TestNewClaudeSessionIDReturnsUUIDv4(t *testing.T) {
	id, err := newClaudeSessionID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 36 || id[14] != '4' || (id[19] != '8' && id[19] != '9' && id[19] != 'a' && id[19] != 'b') {
		t.Fatalf("newClaudeSessionID() = %q, want UUIDv4", id)
	}
}

func TestPrepareClaudeSessionRecoveryUsesGeneratedID(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	newID := "11111111-2222-4333-8444-555555555555"
	writeResumableClaudeSession(t, root, oldID, true)

	gotArgs, recovery := prepareClaudeSessionRecovery(
		[]string{"--resume", oldID},
		root,
		func() (string, error) { return newID, nil },
	)
	if recovery == nil || recovery.NewSessionID != newID {
		t.Fatalf("recovery = %#v, want generated ID %s", recovery, newID)
	}
	if !strings.Contains(strings.Join(gotArgs, "\n"), "--resume\n"+newID) {
		t.Fatalf("generated ID missing from args: %#v", gotArgs)
	}
}

func TestPrepareClaudeSessionRecoveryFailsOpenWhenIDGenerationFails(t *testing.T) {
	root := t.TempDir()
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	writeResumableClaudeSession(t, root, oldID, true)
	args := []string{"--resume", oldID}

	gotArgs, recovery := prepareClaudeSessionRecovery(
		args,
		root,
		func() (string, error) { return "", os.ErrPermission },
	)
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("generation failure did not fail open: args=%v recovery=%#v", gotArgs, recovery)
	}
}

func TestPrepareClaudeSessionRecoveryDoesNotGenerateIDWithoutExplicitResume(t *testing.T) {
	called := false
	args := []string{"--model", "opus"}

	gotArgs, recovery := prepareClaudeSessionRecovery(
		args,
		t.TempDir(),
		func() (string, error) {
			called = true
			return "11111111-2222-4333-8444-555555555555", nil
		},
	)
	if called {
		t.Fatal("session ID generator called without an explicit resume")
	}
	if recovery != nil || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("non-resume args changed: args=%v recovery=%#v", gotArgs, recovery)
	}
}
