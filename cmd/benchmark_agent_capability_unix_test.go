//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

func TestBenchmarkClaudeCapabilityRejectsAHelpCommandThatIgnoresUnknownFlags(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "old-claude")
	// This models Claude's real parser behavior: even a definitely unknown flag
	// exits zero when --help is present. Exit-code probing would call it supported.
	script := "#!/bin/sh\n" +
		"printf '%s\\n' 'Usage: claude [options]' '  --print  Print response'\n" +
		"exit 0\n"
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := benchmarkClaudeIsolationPreflight(&tools.Tool{Name: "old-claude", ExecName: "old-claude"})
	if err == nil || !strings.Contains(err.Error(), "--bare") || !strings.Contains(err.Error(), "claude update") {
		t.Fatalf("preflight error = %v, want missing --bare upgrade error", err)
	}
}
