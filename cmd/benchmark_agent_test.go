package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestBenchmarkAgentUseArgs(t *testing.T) {
	taskFile := filepath.Join(t.TempDir(), "task.txt")
	if err := os.WriteFile(taskFile, []byte("Fix the failing parser test.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		tool string
		want []string
	}{
		{
			name: "claude headless JSON",
			tool: "claude",
			want: []string{"claude", "--model", "model-a", "--", "-p", "Fix the failing parser test.", "--output-format", "json", "--dangerously-skip-permissions"},
		},
		{
			name: "codex exec JSONL",
			tool: "codex",
			want: []string{"codex", "--model", "model-a", "--", "exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "Fix the failing parser test."},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := benchmarkAgentUseArgs([]string{"--tool", test.tool, "--model", "model-a", "--task-file", taskFile})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("args = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestBenchmarkAgentUseArgsRejectsUnreviewedHarnessAndInvalidTask(t *testing.T) {
	taskFile := filepath.Join(t.TempDir(), "task.txt")
	if err := os.WriteFile(taskFile, []byte("task"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := benchmarkAgentUseArgs([]string{"--tool", "pi", "--model", "model-a", "--task-file", taskFile}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unsupported harness error = %v", err)
	}
	if _, err := benchmarkAgentUseArgs([]string{"--tool", "claude", "--model", "model-a", "--task-file", filepath.Join(t.TempDir(), "missing")}); err == nil || !strings.Contains(err.Error(), "read benchmark task") {
		t.Fatalf("missing task error = %v", err)
	}
}

func TestBenchmarkModelSelectionDoesNotBecomeTheRememberedDefault(t *testing.T) {
	settings := &config.Settings{}
	claude, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	catalog := []api.RelayModel{{ID: "benchmark-model", SupportedEndpointTypes: []string{"anthropic"}}}
	selected, err := resolveRememberedModelWithPersistence(
		claude, settings, catalog, "benchmark-model", false, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if selected != "benchmark-model" {
		t.Fatalf("selected = %q", selected)
	}
	if remembered := settings.ToolModel("claude"); remembered != "" {
		t.Fatalf("benchmark persisted model %q", remembered)
	}
}
