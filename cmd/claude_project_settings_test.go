package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

func TestClaudeOwnsBootModelForProjectAndResume(t *testing.T) {
	tool := &tools.Tool{Name: "claude", ExecName: "claude"}
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), []byte(`{"model":"project-model"}`), 0o600); err != nil {
		t.Fatal(err)
	}
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
