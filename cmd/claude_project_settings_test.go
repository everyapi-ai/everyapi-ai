package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

// writeClaudeProjectSettings puts one settings file in a fresh working directory and returns the tool every case here launches.
func writeClaudeProjectSettings(t *testing.T, name, body string) *tools.Tool {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	if body != "" {
		if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".claude", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &tools.Tool{Name: "claude", ExecName: "claude"}
}

func TestClaudeOwnsBootModelForProjectAndResume(t *testing.T) {
	tool := writeClaudeProjectSettings(t, "settings.json", `{"model":"project-model"}`)
	if !claudeOwnsBootModel(tool, []string{"chat"}, "", false) {
		t.Fatal("project model must not be overridden")
	}
	if !claudeOwnsBootModel(tool, []string{"--resume", "session"}, "", false) {
		t.Fatal("resume must not receive a remembered model override")
	}
	if claudeOwnsBootModel(tool, []string{"chat"}, "explicit-model", false) {
		t.Fatal("explicit model should remain authoritative")
	}
}

// A project selecting its model through the settings `env` block is choosing just as deliberately as one setting the top-level field, and an injected --model would outrank both.
func TestClaudeOwnsBootModelForEnvSelectedModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		body string
		want bool
	}{
		{name: "ANTHROPIC_MODEL", file: "settings.json", body: `{"env":{"ANTHROPIC_MODEL":"project-model"}}`, want: true},
		{name: "family alias redirect", file: "settings.json", body: `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"project-opus"}}`, want: true},
		{name: "local settings win too", file: "settings.local.json", body: `{"env":{"ANTHROPIC_MODEL":"project-model"}}`, want: true},
		{name: "blank reads as unset", file: "settings.json", body: `{"env":{"ANTHROPIC_MODEL":"  "}}`, want: false},
		{name: "blank top-level model reads as unset", file: "settings.json", body: `{"model":""}`, want: false},
		{name: "unrelated env stays out of the way", file: "settings.json", body: `{"env":{"FOO":"bar"}}`, want: false},
		{name: "no settings at all", file: "settings.json", body: "", want: false},
		{name: "malformed settings yield to Claude", file: "settings.json", body: `{`, want: true},
		{name: "non-object env yields to Claude", file: "settings.json", body: `{"env":"nope"}`, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := writeClaudeProjectSettings(t, tc.file, tc.body)
			if got := claudeOwnsBootModel(tool, []string{"chat"}, "", false); got != tc.want {
				t.Fatalf("claudeOwnsBootModel = %v, want %v", got, tc.want)
			}
		})
	}
}

// The globally stored dangerous-mode preference must not reach into a project that states its own permissions policy: --dangerously-skip-permissions bypasses that policy rather than merging with it.
func TestClaudeOwnsPermissionMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		body string
		args []string
		want bool
	}{
		{name: "project permissions policy", file: "settings.json", body: `{"permissions":{"deny":["Bash(rm:*)"]}}`, args: []string{"chat"}, want: true},
		{name: "local permissions policy", file: "settings.local.json", body: `{"permissions":{"defaultMode":"plan"}}`, args: []string{"chat"}, want: true},
		{name: "explicit permission mode", file: "settings.json", body: "", args: []string{"--permission-mode", "plan"}, want: true},
		{name: "empty permissions state nothing", file: "settings.json", body: `{"permissions":{}}`, args: []string{"chat"}, want: false},
		{name: "no permissions block", file: "settings.json", body: `{"model":"project-model"}`, args: []string{"chat"}, want: false},
		{name: "no settings at all", file: "settings.json", body: "", args: []string{"chat"}, want: false},
		{name: "malformed settings yield to Claude", file: "settings.json", body: `{`, args: []string{"chat"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := writeClaudeProjectSettings(t, tc.file, tc.body)
			if got := claudeOwnsPermissionMode(tool, tc.args); got != tc.want {
				t.Fatalf("claudeOwnsPermissionMode = %v, want %v", got, tc.want)
			}
		})
	}
}

// Only Claude Code's launch is governed here; every other tool keeps the behavior it had.
func TestClaudeOwnsPermissionModeIgnoresOtherTools(t *testing.T) {
	writeClaudeProjectSettings(t, "settings.json", `{"permissions":{"deny":["Bash(rm:*)"]}}`)
	if claudeOwnsPermissionMode(&tools.Tool{Name: "codex", ExecName: "codex"}, []string{"chat"}) {
		t.Fatal("a project .claude policy must not govern a non-Claude launch")
	}
	if claudeOwnsPermissionMode(nil, []string{"chat"}) {
		t.Fatal("nil tool must not claim project ownership")
	}
}

// The notice explains a flag this launch withheld, so it is only owed when a flag was going to be there: not for a --permission-mode the user typed themselves, and not for a dangerous-mode preference that was never going to add one.
func TestAnnounceProjectOwnedPermissionMode(t *testing.T) {
	enabled, disabled := true, false
	for _, tc := range []struct {
		name          string
		args          []string
		dangerousMode *bool
		interactive   bool
		want          bool
	}{
		{name: "saved yes", args: []string{"chat"}, dangerousMode: &enabled, interactive: true, want: true},
		{name: "saved yes without a tty", args: []string{"chat"}, dangerousMode: &enabled, interactive: false, want: true},
		{name: "saved no has nothing to withhold", args: []string{"chat"}, dangerousMode: &disabled, interactive: true, want: false},
		{name: "unset would have been asked", args: []string{"chat"}, dangerousMode: nil, interactive: true, want: true},
		{name: "unset with no tty is never asked", args: []string{"chat"}, dangerousMode: nil, interactive: false, want: false},
		{name: "explicit permission mode explains itself", args: []string{"--permission-mode", "plan"}, dangerousMode: &enabled, interactive: true, want: false},
		{name: "explicit permission mode in = form", args: []string{"--permission-mode=plan"}, dangerousMode: &enabled, interactive: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := announceProjectOwnedPermissionMode(tc.args, tc.dangerousMode, tc.interactive); got != tc.want {
				t.Fatalf("announceProjectOwnedPermissionMode = %v, want %v", got, tc.want)
			}
		})
	}
}

// The family list behind the env check has to come from the same place the launch overrides do, so a family added there reaches this check with no second edit.
func TestClaudeModelSelectionEnvKeysCoverEveryFamily(t *testing.T) {
	keys := tools.ClaudeModelSelectionEnvKeys()
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		seen[key] = true
	}
	for _, want := range []string{
		"ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL",
	} {
		if !seen[want] {
			t.Errorf("missing %q in %v", want, keys)
		}
	}
}
