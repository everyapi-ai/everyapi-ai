package cmd

import (
	"os"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

// piReasoningCatalog is the shape the reasoning step reads: one model the gateway has verified takes an effort, one it has not.
var piReasoningCatalog = []api.RelayModel{
	{ID: "gpt-5.6-terra", OwnedBy: "openai", SupportedEndpointTypes: []string{"openai", "openai-response"}, SupportsThinking: true},
	{ID: "mystery-model", OwnedBy: "acme", SupportedEndpointTypes: []string{"openai"}},
}

func piTool(t *testing.T) *tools.Tool {
	t.Helper()
	tool, err := tools.Lookup("pi")
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// The remembered level is what a non-interactive launch runs on: there is nobody to ask, and falling back to the tool's default would silently downgrade a scripted run that had been pinned high.
func TestResolveReasoningLevelReusesTheRememberedLevelWhenNobodyCanBeAsked(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(tools.ReasoningLevelEnv, "")
	settings := &config.Settings{ToolReasoningLevels: map[string]string{"pi": "high"}}

	if err := resolveReasoningLevel(piTool(t), settings, piReasoningCatalog, "gpt-5.6-terra", false, true, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(tools.ReasoningLevelEnv); got != "high" {
		t.Fatalf("%s = %q, want high", tools.ReasoningLevelEnv, got)
	}
}

// The remembered level is reused on an interactive launch too, not only when there is nobody to ask. It used to reprompt on every single launch with the cursor parked on the answer already recorded, which made a saved level worth exactly one keystroke less than no saved level at all. Stdin is left as the test binary's own: a picker here would read EOF and return an error, so a passing run is itself the proof that nothing was asked.
func TestResolveReasoningLevelReusesTheRememberedLevelInteractively(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(tools.ReasoningLevelEnv, "")
	settings := &config.Settings{ToolReasoningLevels: map[string]string{"pi": "high"}}

	if err := resolveReasoningLevel(piTool(t), settings, piReasoningCatalog, "gpt-5.6-terra", true, true, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(tools.ReasoningLevelEnv); got != "high" {
		t.Fatalf("%s = %q, want high reused without a prompt", tools.ReasoningLevelEnv, got)
	}
}

// A bare --model reopens the model picker, and the level belongs to the model, so it reopens this step too. Cancelled here (stdin at EOF) it must surface the error rather than silently keep the old level — the user asked to re-choose.
func TestResolveReasoningLevelReopensOnAReask(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(tools.ReasoningLevelEnv, "")
	settings := &config.Settings{ToolReasoningLevels: map[string]string{"pi": "high"}}

	originalStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = reader.Close()
	})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := resolveReasoningLevel(piTool(t), settings, piReasoningCatalog, "gpt-5.6-terra", true, true, true); err == nil {
		t.Fatal("a bare --model did not reopen the reasoning picker")
	}
}

// A level the current model does not offer is dropped rather than forwarded. Pi's scale stops at high; "ultra" is a codex level that reached this entry by a tool switch, and passing it through would put an effort in settings.json that pi clamps away at best.
func TestResolveReasoningLevelDropsALevelTheModelDoesNotOffer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(tools.ReasoningLevelEnv, "")
	settings := &config.Settings{ToolReasoningLevels: map[string]string{"pi": "ultra"}}

	if err := resolveReasoningLevel(piTool(t), settings, piReasoningCatalog, "gpt-5.6-terra", false, true, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(tools.ReasoningLevelEnv); got != "" {
		t.Fatalf("%s = %q, want empty", tools.ReasoningLevelEnv, got)
	}
	if got := settings.ToolReasoningLevel("pi"); got != "" {
		t.Fatalf("stale level survived in settings: %q", got)
	}
}

// No verified effort support, no prompt and no pin — an interactive launch must not stop on a question here either, which is what a non-empty level list would cause.
func TestResolveReasoningLevelSkipsModelsWithoutVerifiedThinking(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(tools.ReasoningLevelEnv, "")
	settings := &config.Settings{ToolReasoningLevels: map[string]string{"pi": "high"}}

	if err := resolveReasoningLevel(piTool(t), settings, piReasoningCatalog, "mystery-model", true, true, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(tools.ReasoningLevelEnv); got != "" {
		t.Fatalf("%s = %q, want empty", tools.ReasoningLevelEnv, got)
	}
}

// A model the live catalogue does not list (an offline --model, an id the gateway dropped) has unknown capabilities, so the step is skipped rather than guessed at.
func TestResolveReasoningLevelSkipsAModelMissingFromTheCatalogue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(tools.ReasoningLevelEnv, "")

	settings := &config.Settings{ToolReasoningLevels: map[string]string{"pi": "high"}}
	for _, id := range []string{"", "not-in-the-catalogue"} {
		if err := resolveReasoningLevel(piTool(t), settings, piReasoningCatalog, id, true, true, false); err != nil {
			t.Fatalf("model %q: %v", id, err)
		}
		if got := os.Getenv(tools.ReasoningLevelEnv); got != "" {
			t.Fatalf("model %q pinned %s = %q, want empty", id, tools.ReasoningLevelEnv, got)
		}
	}
}

// Clients with no level control of their own must reach exec without an extra question, whatever the model supports.
func TestResolveReasoningLevelIsANoOpForClientsWithoutALevelControl(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(tools.ReasoningLevelEnv, "")

	claude, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	catalog := []api.RelayModel{{ID: "claude-opus-5", SupportedEndpointTypes: []string{"anthropic"}, SupportsThinking: true}}
	if err := resolveReasoningLevel(claude, &config.Settings{}, catalog, "claude-opus-5", true, true, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(tools.ReasoningLevelEnv); got != "" {
		t.Fatalf("%s = %q, want empty", tools.ReasoningLevelEnv, got)
	}
}

// launchedModelID has to read each path's own answer: ModelEnv clients export theirs into the environment, the managed-picker clients carry theirs in bootModel.
func TestLaunchedModelIDReadsWhicheverPathChose(t *testing.T) {
	t.Setenv("EVERYAPI_PI_MODEL", "gpt-5.6-terra")
	if got := launchedModelID(piTool(t), "ignored-boot-model"); got != "gpt-5.6-terra" {
		t.Fatalf("ModelEnv tool = %q, want gpt-5.6-terra", got)
	}
	codex, err := tools.Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := launchedModelID(codex, "gpt-5.6-sol"); got != "gpt-5.6-sol" {
		t.Fatalf("managed-picker tool = %q, want gpt-5.6-sol", got)
	}
}

// An inherited level must never reach the launched tool. The variable travels into every child process, so a nested `everyapi use codex` inside a pi session that chose "off" would otherwise write "off" as codex's model_reasoning_effort — a pi word codex does not have. resolveReasoningLevel is the sole author of the variable and clears it on every path that resolves nothing of its own.
func TestResolveReasoningLevelClearsAnInheritedLevel(t *testing.T) {
	claude, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	catalog := []api.RelayModel{{ID: "claude-opus-5", SupportedEndpointTypes: []string{"anthropic"}, SupportsThinking: true}}

	cases := []struct {
		name          string
		tool          *tools.Tool
		modelID       string
		needsEndpoint bool
	}{
		{"client with no level control", claude, "claude-opus-5", true},
		{"model the gateway has not verified", piTool(t), "mystery-model", true},
		{"model missing from the catalogue", piTool(t), "not-in-the-catalogue", true},
		{"metadata-only invocation", piTool(t), "gpt-5.6-terra", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv(tools.ReasoningLevelEnv, "off")

			models := catalog
			if tc.tool.Name == "pi" {
				models = piReasoningCatalog
			}
			if err := resolveReasoningLevel(tc.tool, &config.Settings{}, models, tc.modelID, true, tc.needsEndpoint, false); err != nil {
				t.Fatal(err)
			}
			if got, ok := os.LookupEnv(tools.ReasoningLevelEnv); ok {
				t.Fatalf("%s survived as %q; an inherited level must not reach the tool", tools.ReasoningLevelEnv, got)
			}
		})
	}
}
