package cmd

import (
	"fmt"
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
		{name: "opencode run", tool: "opencode", want: []string{"opencode", "--model", "model-a", "--", "run", "--format", "json", "--auto", "Fix the failing parser test."}},
		{name: "aider message", tool: "aider", want: []string{
			"aider", "--model", "model-a", "--", "--message", "Fix the failing parser test.",
			"--yes-always", "--no-auto-commits",
			"--input-history-file", taskFile + ".aider.input.history",
			"--chat-history-file", taskFile + ".aider.chat.history.md",
			"--llm-history-file", taskFile + ".aider.llm.history",
		}},
		{name: "goose run", tool: "goose", want: []string{"goose", "--model", "model-a", "--", "run", "--text", "Fix the failing parser test.", "--no-session", "--stats", "--output-format", "json"}},
		{name: "crush run", tool: "crush", want: []string{"crush", "--model", "model-a", "--", "run", "--quiet", "Fix the failing parser test."}},
		{name: "cline prompt", tool: "cline", want: []string{"cline", "--model", "model-a", "--", "Fix the failing parser test.", "--json", "--auto-approve", "true"}},
		{name: "openclaw local agent", tool: "openclaw", want: []string{"openclaw", "--model", "model-a", "--", "agent", "--local", "--message", "Fix the failing parser test.", "--json"}},
		{name: "continue print", tool: "continue", want: []string{"continue", "--model", "model-a", "--", "--print", "Fix the failing parser test.", "--auto", "--format", "json"}},
		{name: "kilo run", tool: "kilo", want: []string{"kilo", "--model", "model-a", "--", "run", "--format", "json", "--auto", "Fix the failing parser test."}},
		{name: "pi print", tool: "pi", want: []string{"pi", "--model", "model-a", "--", "--print", "--mode", "json", "--approve", "Fix the failing parser test."}},
		{name: "vibe prompt", tool: "vibe", want: []string{"vibe", "--model", "model-a", "--", "--prompt", "Fix the failing parser test.", "--output", "streaming", "--auto-approve", "--trust"}},
		{name: "copilot prompt", tool: "copilot", want: []string{"copilot", "--model", "model-a", "--", "--prompt", "Fix the failing parser test.", "--output-format", "json", "--allow-all"}},
		{name: "droid exec", tool: "droid", want: []string{"droid", "--model", "model-a", "--", "exec", "--output-format", "stream-json", "--skip-permissions-unsafe", "Fix the failing parser test."}},
		{name: "openhands headless", tool: "openhands", want: []string{"openhands", "--model", "model-a", "--", "--override-with-envs", "--headless", "--json", "--always-approve", "--task", "Fix the failing parser test."}},
		{name: "forge prompt", tool: "forge", want: []string{"forge", "--model", "model-a", "--", "--prompt", "Fix the failing parser test."}},
		{name: "grok single", tool: "grok", want: []string{"grok", "--model", "model-a", "--", "--single", "Fix the failing parser test.", "--output-format", "streaming-messages-json", "--always-approve"}},
		{name: "qwen prompt", tool: "qwen_code", want: []string{"qwen-code", "--model", "model-a", "--", "--prompt", "Fix the failing parser test.", "--output-format", "stream-json", "--yolo"}},
		{name: "kimi prompt", tool: "kimi_code", want: []string{"kimi-code", "--model", "model-a", "--", "--prompt", "Fix the failing parser test.", "--output-format", "stream-json", "--auto"}},
		{name: "hermes oneshot", tool: "hermes", want: []string{"hermes", "--model", "model-a", "--", "--oneshot", "Fix the failing parser test.", "--yolo", "--accept-hooks"}},
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
	if _, err := benchmarkAgentUseArgs([]string{"--tool", "open-webui", "--model", "model-a", "--task-file", taskFile}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unsupported harness error = %v", err)
	}
	if _, err := benchmarkAgentUseArgs([]string{"--tool", "claude", "--model", "model-a", "--task-file", filepath.Join(t.TempDir(), "missing")}); err == nil || !strings.Contains(err.Error(), "read benchmark task") {
		t.Fatalf("missing task error = %v", err)
	}
}

func TestBenchmarkCatalogIncludesReviewedHarnessesAndCompatibleModels(t *testing.T) {
	catalog := []api.RelayModel{
		{ID: "anthropic-model", SupportedEndpointTypes: []string{"anthropic"}},
		{ID: "chat-model", SupportedEndpointTypes: []string{"openai"}},
		{ID: "responses-model", SupportedEndpointTypes: []string{"openai-response"}},
		{ID: "image-model", SupportedEndpointTypes: []string{"image-generation"}},
	}

	got := benchmarkCatalog(catalog)
	models := make(map[string][]string, len(got.Harnesses))
	for _, harness := range got.Harnesses {
		models[harness.Harness] = harness.Models
	}

	if !reflect.DeepEqual(models["claude"], []string{"anthropic-model"}) {
		t.Fatalf("claude models = %v", models["claude"])
	}
	if !reflect.DeepEqual(models["codex"], []string{"responses-model"}) {
		t.Fatalf("codex models = %v", models["codex"])
	}
	if !reflect.DeepEqual(models["opencode"], []string{"chat-model", "responses-model"}) {
		t.Fatalf("opencode models = %v", models["opencode"])
	}
	if _, exists := models["open-webui"]; exists {
		t.Fatal("server-only Open WebUI was exposed as a benchmark harness")
	}
	if _, exists := models["qwen_code"]; !exists {
		t.Fatal("desktop Qwen harness identifier was not exposed")
	}
}

func TestBenchmarkCatalogDeterministicallyBoundsLargeCompatibleCatalogs(t *testing.T) {
	catalog := make([]api.RelayModel, 300)
	for index := range catalog {
		catalog[index] = api.RelayModel{
			ID:                     fmt.Sprintf("model-%03d", index),
			SupportedEndpointTypes: []string{"openai"},
		}
	}

	got := benchmarkCatalog(catalog)
	for _, harness := range got.Harnesses {
		if harness.Harness != "aider" {
			continue
		}
		if len(harness.Models) != maxBenchmarkModelsPerHarness {
			t.Fatalf("aider model count = %d, want %d", len(harness.Models), maxBenchmarkModelsPerHarness)
		}
		if !harness.Truncated {
			t.Fatal("large aider catalog did not signal truncation")
		}
		if harness.Models[0] != "model-000" || harness.Models[len(harness.Models)-1] != "model-063" {
			t.Fatalf("bounded models are not deterministic: %v", harness.Models)
		}
		return
	}
	t.Fatal("aider benchmark catalog entry is missing")
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
