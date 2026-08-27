package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
)

func writeClaudeSettings(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readClaudeSettings(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func captureClaudeSettingsOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	return &out
}

// The bug this guards: `/model` inside a gateway launch saves its pick as the user's default for every future ordinary session, where a gateway-only id does not resolve.
func TestClaudeModelRestoreUndoesAnInSessionPick(t *testing.T) {
	dir := writeClaudeSettings(t, "{\n  \"model\": \"claude-fable-5[1m]\",\n  \"theme\": \"auto\"\n}\n")
	out := captureClaudeSettingsOutput(t)
	snap := snapshotClaudeUserModel(dir)
	if snap == nil {
		t.Fatal("expected a snapshot for a readable settings file")
	}

	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{\n  \"model\": \"qwen2.5:7b\",\n  \"theme\": \"auto\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap.restore()

	if got := readClaudeSettings(t, dir); got != "{\n  \"model\": \"claude-fable-5[1m]\",\n  \"theme\": \"auto\"\n}\n" {
		t.Fatalf("model not restored: %q", got)
	}
	if !strings.Contains(out.String(), "Restored the `model` field") {
		t.Fatalf("expected the restore to be announced, got %q", out.String())
	}
}

// A launch where nobody opened the picker must not touch the file at all — rewriting it would churn a hand-maintained config on every launch.
func TestClaudeModelRestoreLeavesAnUnchangedFileByte(t *testing.T) {
	body := "{\n\t\"model\": \"opus\",\n\t\"hooks\": {\n\t\t\"Stop\": []\n\t}\n}\n"
	dir := writeClaudeSettings(t, body)
	out := captureClaudeSettingsOutput(t)
	snap := snapshotClaudeUserModel(dir)
	if snap == nil {
		t.Fatal("expected a snapshot for a readable settings file")
	}
	before, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	snap.restore()

	if got := readClaudeSettings(t, dir); got != body {
		t.Fatalf("file rewritten: %q", got)
	}
	after, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("file was rewritten despite an unchanged model")
	}
	if out.String() != "" {
		t.Fatalf("expected no announcement, got %q", out.String())
	}
}

// A user with no `model` at all is on Claude Code's own default; a pick made inside the launch must not leave one behind.
func TestClaudeModelRestoreRemovesAFieldTheUserNeverHad(t *testing.T) {
	dir := writeClaudeSettings(t, "{\n  \"theme\": \"auto\",\n  \"tui\": \"fullscreen\"\n}\n")
	captureClaudeSettingsOutput(t)
	snap := snapshotClaudeUserModel(dir)
	if snap == nil {
		t.Fatal("expected a snapshot for a readable settings file")
	}

	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{\n  \"theme\": \"auto\",\n  \"tui\": \"fullscreen\",\n  \"model\": \"qwen2.5:7b\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap.restore()

	if got := readClaudeSettings(t, dir); got != "{\n  \"theme\": \"auto\",\n  \"tui\": \"fullscreen\"\n}\n" {
		t.Fatalf("model not removed: %q", got)
	}
}

// A pick that also DELETED the field (Claude Code writes no `model` when the user selects the recommended default) has to put the user's value back where it was, not append it after the keys that followed it.
func TestClaudeModelRestoreReinsertsAtItsOriginalPosition(t *testing.T) {
	dir := writeClaudeSettings(t, "{\n  \"model\": \"opus\",\n  \"theme\": \"auto\",\n  \"tui\": \"fullscreen\"\n}\n")
	captureClaudeSettingsOutput(t)
	snap := snapshotClaudeUserModel(dir)
	if snap == nil {
		t.Fatal("expected a snapshot for a readable settings file")
	}

	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{\n  \"theme\": \"auto\",\n  \"tui\": \"fullscreen\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap.restore()

	if got := readClaudeSettings(t, dir); got != "{\n  \"model\": \"opus\",\n  \"theme\": \"auto\",\n  \"tui\": \"fullscreen\"\n}\n" {
		t.Fatalf("model not reinserted in place: %q", got)
	}
}

// Everything the session legitimately changed stays changed: the restore is scoped to one field, not to the file.
func TestClaudeModelRestoreKeepsOtherEditsFromTheSession(t *testing.T) {
	dir := writeClaudeSettings(t, "{\n  \"model\": \"opus\",\n  \"theme\": \"auto\",\n  \"permissions\": {\n    \"allow\": []\n  }\n}\n")
	captureClaudeSettingsOutput(t)
	snap := snapshotClaudeUserModel(dir)
	if snap == nil {
		t.Fatal("expected a snapshot for a readable settings file")
	}

	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{\n  \"model\": \"qwen2.5:7b\",\n  \"theme\": \"dark\",\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(go test:*)\"\n    ]\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap.restore()

	got := readClaudeSettings(t, dir)
	if !strings.Contains(got, "\"model\": \"opus\"") {
		t.Fatalf("model not restored: %q", got)
	}
	if !strings.Contains(got, "\"theme\": \"dark\"") || !strings.Contains(got, "Bash(go test:*)") {
		t.Fatalf("unrelated session edits were reverted: %q", got)
	}
}

// ExecWithOptions runs the cleanup chain on both the start-failure and the child-exited path, and the chain overlaps a deferred call; a second restore must not print a second notice.
func TestClaudeModelRestoreRunsOnce(t *testing.T) {
	dir := writeClaudeSettings(t, "{\n  \"model\": \"opus\"\n}\n")
	out := captureClaudeSettingsOutput(t)
	snap := snapshotClaudeUserModel(dir)
	if snap == nil {
		t.Fatal("expected a snapshot for a readable settings file")
	}

	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{\n  \"model\": \"qwen2.5:7b\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap.restore()
	snap.restore()

	if n := strings.Count(out.String(), "Restored the `model` field"); n != 1 {
		t.Fatalf("expected exactly one announcement, got %d", n)
	}
}

// A file this cannot parse is a file it must not rewrite, so no snapshot is taken and the launch proceeds without a restore.
func TestSnapshotClaudeUserModelDeclinesWhatItCannotRewrite(t *testing.T) {
	for name, body := range map[string]string{
		"invalid JSON":     "{ not json",
		"not an object":    "[1, 2, 3]",
		"trailing content": "{\"model\": \"opus\"} trailing",
	} {
		t.Run(name, func(t *testing.T) {
			if snap := snapshotClaudeUserModel(writeClaudeSettings(t, body)); snap != nil {
				t.Fatalf("expected no snapshot for %s", name)
			}
		})
	}
	if snap := snapshotClaudeUserModel(""); snap != nil {
		t.Fatal("expected no snapshot without a configuration directory")
	}
	if snap := snapshotClaudeUserModel(t.TempDir()); snap != nil {
		t.Fatal("expected no snapshot when the settings file is absent")
	}
}

// Non-Claude launches never resolve a configuration directory, so the launch cleanup chain must stay nil for them rather than gaining a no-op that routes every tool through ExecWithOptions.
func TestSnapshotClaudeUserModelKeepsFilePermissions(t *testing.T) {
	dir := writeClaudeSettings(t, "{\n  \"model\": \"opus\"\n}\n")
	captureClaudeSettingsOutput(t)
	path := filepath.Join(dir, "settings.json")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	snap := snapshotClaudeUserModel(dir)
	if snap == nil {
		t.Fatal("expected a snapshot for a readable settings file")
	}

	if err := os.WriteFile(path, []byte("{\n  \"model\": \"qwen2.5:7b\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap.restore()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions widened to %v", info.Mode().Perm())
	}
}
