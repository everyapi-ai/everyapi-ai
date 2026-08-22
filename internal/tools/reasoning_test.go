package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// stubCodexReasoningCatalog stands in for `codex debug models --bundled` with two slugs that disagree about which efforts they take, which is the whole reason the levels are read per model rather than hard-coded.
func stubCodexReasoningCatalog(t *testing.T) {
	t.Helper()
	original := codexBundledCatalog
	codexBundledCatalog = func() ([]byte, error) {
		return []byte(`{"models":[
			{"slug":"gpt-deep","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}]},
			{"slug":"gpt-shallow","default_reasoning_level":"low","supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}]},
			{"slug":"gpt-flat","default_reasoning_level":null,"supported_reasoning_levels":[]}
		]}`), nil
	}
	t.Cleanup(func() { codexBundledCatalog = original })
}

func TestReasoningLevelsReadCodexPerModelSupport(t *testing.T) {
	stubCodexReasoningCatalog(t)
	codex, err := Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}

	levels, preferred := ReasoningLevels(codex, Model{ID: "gpt-deep"})
	if want := []string{"low", "medium", "high", "xhigh"}; !slices.Equal(levels, want) {
		t.Fatalf("gpt-deep levels = %v, want %v", levels, want)
	}
	if preferred != "medium" {
		t.Fatalf("gpt-deep default level = %q, want medium", preferred)
	}

	// A shorter list is the point: offering a level Codex does not accept for this slug would pin an effort its own picker never shows.
	if levels, _ := ReasoningLevels(codex, Model{ID: "gpt-shallow"}); !slices.Equal(levels, []string{"low", "high"}) {
		t.Fatalf("gpt-shallow levels = %v, want [low high]", levels)
	}
	// A model Codex publishes with no efforts, and one it has never heard of, both mean "no level step" — matching the catalogue EveryAPI writes for them.
	if levels, _ := ReasoningLevels(codex, Model{ID: "gpt-flat"}); len(levels) != 0 {
		t.Fatalf("gpt-flat levels = %v, want none", levels)
	}
	if levels, _ := ReasoningLevels(codex, Model{ID: "minimax-unknown-to-codex"}); len(levels) != 0 {
		t.Fatalf("unknown slug levels = %v, want none", levels)
	}
}

// TestCodexReasoningLevelsSurviveAnUnusableCodex pins the fail-soft contract: this call decides whether to ask one extra question, so a codex that cannot be executed must degrade to "no level step", never to a failed launch.
func TestCodexReasoningLevelsSurviveAnUnusableCodex(t *testing.T) {
	original := codexBundledCatalog
	codexBundledCatalog = func() ([]byte, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { codexBundledCatalog = original })

	codex, _ := Lookup("codex")
	if levels, preferred := ReasoningLevels(codex, Model{ID: "gpt-deep"}); len(levels) != 0 || preferred != "" {
		t.Fatalf("levels with no usable codex = %v/%q, want empty", levels, preferred)
	}
}

func TestReasoningLevelsForPiFollowGatewayThinkingSupport(t *testing.T) {
	pi, err := Lookup("pi")
	if err != nil {
		t.Fatal(err)
	}
	levels, preferred := ReasoningLevels(pi, Model{ID: "gpt-5.6-terra", SupportsThinking: true})
	if want := []string{"off", "minimal", "low", "medium", "high"}; !slices.Equal(levels, want) {
		t.Fatalf("pi standard levels = %v, want %v", levels, want)
	}
	if preferred != "medium" {
		t.Fatalf("pi default level = %q, want medium", preferred)
	}
	lunaLevels, preferred := ReasoningLevels(pi, Model{ID: "gpt-5.6-luna", SupportsThinking: true})
	if want := []string{"off", "low", "medium", "high", "xhigh", "max"}; !slices.Equal(lunaLevels, want) {
		t.Fatalf("pi Luna levels = %v, want %v", lunaLevels, want)
	}
	if preferred != "medium" {
		t.Fatalf("pi Luna default level = %q, want medium", preferred)
	}
	// Unverified support is not a denial, but it is not permission either: an effort sent to a model the gateway has not confirmed can 400 the first request.
	if levels, _ := ReasoningLevels(pi, Model{ID: "mystery-model"}); len(levels) != 0 {
		t.Fatalf("pi levels for an unverified model = %v, want none", levels)
	}
}

func TestReasoningLevelsAreEmptyForClientsWithoutALevelControl(t *testing.T) {
	for _, name := range []string{"claude", "opencode", "crush"} {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		if levels, _ := ReasoningLevels(tool, Model{ID: "gpt-5.6-terra", SupportsThinking: true}); len(levels) != 0 {
			t.Fatalf("%s offered reasoning levels %v, want none", name, levels)
		}
	}
}

// TestPrepareCodexPinsTheSelectedReasoningEffort is the fix for the asymmetry users hit: the model survived a relaunch (it is remembered in settings.json) while the effort did not, because codex writes it into the config.toml inside a CODEX_HOME that is deleted on exit.
func TestPrepareCodexPinsTheSelectedReasoningEffort(t *testing.T) {
	codexTestHome(t)
	stubCodexBundledCatalog(t)
	t.Setenv(ReasoningLevelEnv, "xhigh")

	env, err := prepareCodexWithModels("https://api.everyapi.ai", "tok", []Model{{ID: "gpt-5.6-sol"}}, "gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	configPath := preparedCodexConfigPath(t, env)
	t.Cleanup(TakePreparedCleanup(env))
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `model_reasoning_effort = "xhigh"`) {
		t.Fatalf("injected codex config missing the selected effort:\n%s", body)
	}
}

// Transparent mode is the DEFAULT for codex, so a fix that only covered the injected path would leave the plain `everyapi use codex` still booting at the model's own default.
func TestPrepareCodexTransparentPinsTheSelectedReasoningEffort(t *testing.T) {
	codexTestHome(t)
	stubCodexBundledCatalog(t)
	t.Setenv(ReasoningLevelEnv, "high")

	env, err := prepareCodexTransparentWithModels([]Model{{ID: "gpt-5.6-sol"}}, "gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	configPath := preparedCodexConfigPath(t, env)
	t.Cleanup(TakePreparedCleanup(env))
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `model_reasoning_effort = "high"`) {
		t.Fatalf("transparent codex config missing the selected effort:\n%s", body)
	}
}

// An unset variable must change nothing: scripted launches and the legacy fixed-home path never pass through the picker.
func TestPrepareCodexLeavesEffortAloneWithoutASelection(t *testing.T) {
	// Pinned empty rather than assumed empty: the variable travels into launched tools, so a developer running this from inside an `everyapi use` session would otherwise have it set, and the test certifies "an unset variable changes nothing" without ever establishing that it is unset.
	t.Setenv(ReasoningLevelEnv, "")
	codexTestHome(t)
	stubCodexBundledCatalog(t)

	env, err := prepareCodexWithModels("https://api.everyapi.ai", "tok", []Model{{ID: "gpt-5.6-sol"}}, "gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	configPath := preparedCodexConfigPath(t, env)
	t.Cleanup(TakePreparedCleanup(env))
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// Matched as an assignment: the generated file's header comment mentions the key by name.
	if strings.Contains(string(body), "model_reasoning_effort = ") {
		t.Fatalf("codex config pinned an effort nobody chose:\n%s", body)
	}
}

// TestPiPrepareDeclaresThinkingSupportAndWindows covers the reason pi showed `thinking: no` for every EveryAPI model, GPT-5.x included: an undeclared model is reasoning:false to pi, which disables its thinking-level control outright.
func TestPiPrepareDeclaresThinkingSupportAndWindows(t *testing.T) {
	t.Setenv(piModelEnv, "gpt-5.6-terra")
	t.Setenv(ReasoningLevelEnv, "high")
	tool, _ := Lookup("pi")

	models := []Model{
		{ID: "gpt-5.6-terra", SupportedEndpointTypes: []string{"openai-response"}, SupportsThinking: true, ContextWindow: 400000, MaxOutput: 128000},
		{ID: "gpt-5.6-luna", SupportedEndpointTypes: []string{"openai-response"}, SupportsThinking: true},
		{ID: "mystery-model", SupportedEndpointTypes: []string{"openai"}},
	}
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", models, "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()

	body, err := os.ReadFile(filepath.Join(extra[piAgentDirEnv], "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Providers map[string]struct {
			Models []map[string]any `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, model := range config.Providers["everyapi"].Models {
		byID[model["id"].(string)] = model
	}
	thinking := byID["gpt-5.6-terra"]
	if thinking["reasoning"] != true {
		t.Fatalf("verified model did not declare reasoning support: %#v", thinking)
	}
	if thinking["contextWindow"] != float64(400000) || thinking["maxTokens"] != float64(128000) {
		t.Fatalf("model window/output not carried through: %#v", thinking)
	}
	// Standard models omit thinkingLevelMap so pi maps off→high onto its own provider defaults and keeps unverified xhigh/max hidden.
	if _, declared := thinking["thinkingLevelMap"]; declared {
		t.Fatalf("standard model declared a thinking level map: %#v", thinking)
	}
	luna := byID["gpt-5.6-luna"]
	wantLunaLevels := map[string]any{
		"off": "none", "minimal": nil, "low": "low", "medium": "medium",
		"high": "high", "xhigh": "xhigh", "max": "max",
	}
	if got := luna["thinkingLevelMap"]; !reflect.DeepEqual(got, wantLunaLevels) {
		t.Fatalf("Luna thinking level map = %#v, want %#v", got, wantLunaLevels)
	}
	if _, declared := byID["mystery-model"]["reasoning"]; declared {
		t.Fatalf("unverified model declared reasoning support: %#v", byID["mystery-model"])
	}

	settings, err := os.ReadFile(filepath.Join(extra[piAgentDirEnv], "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), `"defaultThinkingLevel":"high"`) {
		t.Fatalf("Pi settings missing the selected thinking level: %s", settings)
	}
}

func TestPiPrepareOmitsThinkingLevelWithoutASelection(t *testing.T) {
	t.Setenv(ReasoningLevelEnv, "")
	t.Setenv(piModelEnv, "gpt-5.6-terra")
	tool, _ := Lookup("pi")

	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key",
		[]Model{{ID: "gpt-5.6-terra", SupportedEndpointTypes: []string{"openai"}, SupportsThinking: true}}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	settings, err := os.ReadFile(filepath.Join(extra[piAgentDirEnv], "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settings), "defaultThinkingLevel") {
		t.Fatalf("Pi settings pinned a thinking level nobody chose: %s", settings)
	}
}

// PersistedReasoningLevel is what makes the first launch after this feature shipped non-destructive. inheritPersistentCodexReasoningEffort feeds the EveryAPI-owned home's effort into every fresh lifecycle-bound profile, and applySelectedCodexReasoningEffort now outranks it — so unless the picker opens with that value on the cursor, a user who had chosen "high" long ago sees Codex's own per-slug default ("low" for gpt-5.6-sol) preselected and one distracted Enter downgrades them.
func TestPersistedReasoningLevelReadsTheCodexHomeTheLauncherInherits(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"),
		[]byte("model = \"gpt-5.6-sol\"\nmodel_reasoning_effort = \"high\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	codex, err := Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := PersistedReasoningLevel(codex); got != "high" {
		t.Fatalf("PersistedReasoningLevel = %q, want high", got)
	}
}

// Everything else keeps none: pi's agent dir is process-scoped and deleted on exit, and a client with no level control has nothing to read back. A non-empty answer here would put a foreign vocabulary on the picker's cursor.
func TestPersistedReasoningLevelIsEmptyForClientsThatKeepNone(t *testing.T) {
	codexTestHome(t)
	for _, name := range []string{"pi", "claude"} {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := PersistedReasoningLevel(tool); got != "" {
			t.Fatalf("%s: PersistedReasoningLevel = %q, want empty", name, got)
		}
	}
	if got := PersistedReasoningLevel(nil); got != "" {
		t.Fatalf("nil tool: PersistedReasoningLevel = %q, want empty", got)
	}
}

// SupportsReasoningLevels answers the model-free half of the question, so the two functions relate one way only: a non-empty level list proves the client has a control, while a client that has one can still return no levels for a model it does not recognise (codex, for a slug missing from its bundled catalogue). What must never happen is the reverse — a client with no control producing levels.
func TestSupportsReasoningLevelsMatchesWhoCanProduceLevels(t *testing.T) {
	stubCodexBundledCatalog(t)
	models := []Model{
		{ID: "gpt-5.6-sol", SupportsThinking: true},
		{ID: "gpt-template", SupportsThinking: true},
		{ID: "mystery-model"},
	}
	for _, name := range []string{"codex", "pi", "claude", "gemini"} {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		supported := SupportsReasoningLevels(tool)
		for _, model := range models {
			levels, _ := ReasoningLevels(tool, model)
			if len(levels) > 0 && !supported {
				t.Fatalf("%s/%s: produced %d levels while SupportsReasoningLevels said no", name, model.ID, len(levels))
			}
		}
		if !supported {
			continue
		}
		if name != "codex" && name != "pi" {
			t.Fatalf("%s: unexpected level control; the registry is the documented list", name)
		}
	}
	if SupportsReasoningLevels(nil) {
		t.Fatal("nil tool reported a level control")
	}
}

// The registry is what keeps the two functions from drifting, so pin that both documented clients are in it — a client silently dropped would make SupportsReasoningLevels quietly skip its prompt forever.
func TestSupportsReasoningLevelsCoversBothDocumentedClients(t *testing.T) {
	for _, name := range []string{"codex", "pi"} {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		if !SupportsReasoningLevels(tool) {
			t.Fatalf("%s lost its level control", name)
		}
	}
}
