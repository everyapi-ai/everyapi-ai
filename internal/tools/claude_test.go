package tools

import (
	"reflect"
	"testing"
)

func modelsFromIDs(ids ...string) []Model {
	models := make([]Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, Model{ID: id})
	}
	return models
}

// assertClaudeEnv checks that every variable named in want carries that value, and that every other override variable is blanked. The blanks are part of the contract: a family the catalogue does not serve must not inherit an ANTHROPIC_DEFAULT_* export from the user's shell.
func assertClaudeEnv(t *testing.T, got, want map[string]string) {
	t.Helper()
	full := make(map[string]string, len(claudeFamilies)*2)
	for _, spec := range claudeFamilies {
		full[spec.modelEnv] = ""
		full[spec.nameEnv] = ""
	}
	for key, value := range want {
		full[key] = value
	}
	if !reflect.DeepEqual(got, full) {
		t.Fatalf("claudeFamilyDefaultEnv() = %#v, want %#v", got, full)
	}
}

// The catalogue EveryAPI actually serves today. Without these overrides Claude Code's gateway tier resolves opus to claude-opus-4-7 and sonnet to claude-sonnet-4-6, neither of which is routable here.
func TestClaudeFamilyDefaultEnvPinsEveryFamilyInTheCatalogue(t *testing.T) {
	got := claudeFamilyDefaultEnv(modelsFromIDs("claude-fable-5", "claude-haiku-4-5", "claude-opus-5", "claude-sonnet-5"))
	want := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":        "claude-opus-5",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":   "Opus 5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":      "claude-sonnet-5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "Sonnet 5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":       "claude-haiku-4-5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME":  "Haiku 4.5",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":       "claude-fable-5",
		"ANTHROPIC_DEFAULT_FABLE_MODEL_NAME":  "Fable 5",
	}
	assertClaudeEnv(t, got, want)
}

func TestClaudeFamilyDefaultEnvPicksNewestPerFamily(t *testing.T) {
	// Catalogue order is the picker's preferred order, not a version order, so the newest must win regardless of position.
	got := claudeFamilyDefaultEnv(modelsFromIDs(
		"claude-opus-4-7", "claude-opus-5", "claude-opus-4-8",
		"claude-sonnet-4-6", "claude-sonnet-5",
	))
	if got["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-5" {
		t.Errorf("opus = %q, want claude-opus-5", got["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
	if got["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "claude-sonnet-5" {
		t.Errorf("sonnet = %q, want claude-sonnet-5", got["ANTHROPIC_DEFAULT_SONNET_MODEL"])
	}
}

func TestClaudeFamilyDefaultEnvComparesVersionsNumericallyNotLexically(t *testing.T) {
	// "4-10" sorts before "4-9" as text; only a numeric comparison gets this right.
	got := claudeFamilyDefaultEnv(modelsFromIDs("claude-opus-4-9", "claude-opus-4-10"))
	if got["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-4-10" {
		t.Errorf("opus = %q, want claude-opus-4-10", got["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
	if got["ANTHROPIC_DEFAULT_OPUS_MODEL_NAME"] != "Opus 4.10" {
		t.Errorf("opus name = %q, want %q", got["ANTHROPIC_DEFAULT_OPUS_MODEL_NAME"], "Opus 4.10")
	}
}

// A trailing YYYYMMDD is a build stamp, not a version segment. Anthropic publishes claude-opus-4-20250514 for Opus 4 and claude-opus-4-6 for Opus 4.6 — both gateway-facing ids in modelcaps' catalogue — so comparing the date as a minor version would make the May 2025 build outrank every 4.x release after it, which is the same "retired model offered as current" failure this file exists to prevent.
func TestClaudeFamilyDefaultEnvTreatsAReleaseDateAsABuildNotAVersion(t *testing.T) {
	got := claudeFamilyDefaultEnv(modelsFromIDs("claude-opus-4-20250514", "claude-opus-4-6", "claude-sonnet-4-20250514", "claude-sonnet-4-5"))
	want := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":        "claude-opus-4-6",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":   "Opus 4.6",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":      "claude-sonnet-4-5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "Sonnet 4.5",
	}
	assertClaudeEnv(t, got, want)
}

// Within one generation the rolling id wins: it is the alias Anthropic keeps pointed at the current build, and it labels the picker entry "Haiku 4.5" rather than naming a build date nobody chose.
func TestClaudeFamilyDefaultEnvPrefersTheRollingIDOverASnapshotOfTheSameGeneration(t *testing.T) {
	got := claudeFamilyDefaultEnv(modelsFromIDs("claude-haiku-4-5-20251001", "claude-haiku-4-5"))
	want := map[string]string{
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":      "claude-haiku-4-5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME": "Haiku 4.5",
	}
	assertClaudeEnv(t, got, want)
}

// A Bedrock/Vertex-only key never sees a rolling id — those upstreams publish dated builds exclusively — so the newest snapshot must still win, and still label itself by generation.
func TestClaudeFamilyDefaultEnvRanksSnapshotsByDateWhenNoRollingIDExists(t *testing.T) {
	got := claudeFamilyDefaultEnv(modelsFromIDs("claude-sonnet-4-5-20250929", "claude-sonnet-4-5-20250101"))
	want := map[string]string{
		"ANTHROPIC_DEFAULT_SONNET_MODEL":      "claude-sonnet-4-5-20250929",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "Sonnet 4.5",
	}
	assertClaudeEnv(t, got, want)
}

// cliout.Sanitize strips control bytes from a catalogue id but leaves surrounding spaces, and the parse trims before it reads the family. The value handed to Claude Code has to be trimmed too, or the alias resolves to an id with a space in it that the gateway cannot route.
func TestClaudeFamilyDefaultEnvTrimsTheCatalogueID(t *testing.T) {
	got := claudeFamilyDefaultEnv(modelsFromIDs("  claude-opus-5  "))
	if got["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-5" {
		t.Errorf("opus = %q, want claude-opus-5", got["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
}

// The whole point of reading the catalogue is that a model released after this code was written is picked up without editing it. Nothing here may need a new constant when the next Opus ships.
func TestClaudeFamilyDefaultEnvFollowsModelsThisCodeHasNeverSeen(t *testing.T) {
	got := claudeFamilyDefaultEnv(modelsFromIDs("claude-opus-5", "claude-opus-6", "claude-sonnet-7-2"))
	want := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":        "claude-opus-6",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":   "Opus 6",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":      "claude-sonnet-7-2",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "Sonnet 7.2",
	}
	assertClaudeEnv(t, got, want)
}

// A catalogue that has retired the current generation must resolve to what it actually serves, not to a newer id this code happens to know the name of.
func TestClaudeFamilyDefaultEnvFollowsTheCatalogueDownwards(t *testing.T) {
	got := claudeFamilyDefaultEnv(modelsFromIDs("claude-opus-4-7"))
	want := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":      "claude-opus-4-7",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME": "Opus 4.7",
	}
	assertClaudeEnv(t, got, want)
}

// An ANTHROPIC_DEFAULT_* export in the user's shell reaches the child through mergeEnv, so a family the catalogue does not serve has to be blanked rather than omitted. An empty value reads as unset to Claude Code, which then applies its own resolution instead of whatever that machine had configured.
func TestClaudeFamilyDefaultEnvBlanksFamiliesTheCatalogueLacks(t *testing.T) {
	got := claudeFamilyDefaultEnv(modelsFromIDs("claude-sonnet-5"))
	for _, family := range []string{"opus", "haiku", "fable"} {
		spec := claudeFamilies[family]
		value, present := got[spec.modelEnv]
		if !present {
			t.Errorf("%s absent from the overlay; an ambient export would survive the launch", spec.modelEnv)
			continue
		}
		if value != "" {
			t.Errorf("%s = %q, want blank", spec.modelEnv, value)
		}
	}
	if got[claudeFamilies["sonnet"].modelEnv] != "claude-sonnet-5" {
		t.Errorf("sonnet = %q, want claude-sonnet-5", got[claudeFamilies["sonnet"].modelEnv])
	}
}

// The model-less Prepare path carries no catalogue at all. That is missing information, not a catalogue that serves nothing, so it must not blank anything.
func TestClaudeFamilyDefaultEnvLeavesTheEnvironmentAloneWithoutACatalogue(t *testing.T) {
	if got := claudeFamilyDefaultEnv(nil); got != nil {
		t.Errorf("claudeFamilyDefaultEnv(nil) = %#v, want nil", got)
	}
}

func TestClaudeFamilyDefaultEnvSkipsFamiliesTheCatalogueLacks(t *testing.T) {
	// A relay key scoped to Sonnet alone must not have opus pointed at a model it cannot route; Claude Code keeps its own resolution for the untouched families.
	got := claudeFamilyDefaultEnv(modelsFromIDs("claude-sonnet-5"))
	want := map[string]string{
		"ANTHROPIC_DEFAULT_SONNET_MODEL":      "claude-sonnet-5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "Sonnet 5",
	}
	assertClaudeEnv(t, got, want)
}

func TestClaudeFamilyDefaultEnvIgnoresNonFamilyEntries(t *testing.T) {
	// The launch catalogue carries every chat-capable model the key can reach, including non-Anthropic ones and suffixed Anthropic aliases. None may become a family default — the catalogue was read, so every override is blanked rather than left to an ambient export.
	for _, id := range []string{
		"gpt-5.6-sol",
		"claude-opus-4-7-thinking",
		"claude-3-5-sonnet",
		"claude-3-opus-latest",
		"claude-opus",
		// Last deliberately. Our own brand name carries the substring "api", which gitleaks' generic-api-key rule reads as its keyword and then scores the NEXT quoted element as the trailing secret — the brand-name-as-keyword false positive .gitleaks.toml already documents twice. Keeping this entry at the end of the list leaves no quoted value after it to be misread.
		"claude-everyapi-gpt-5-6-sol",
	} {
		got := claudeFamilyDefaultEnv(modelsFromIDs(id))
		for key, value := range got {
			if value != "" {
				t.Errorf("claudeFamilyDefaultEnv(%q) set %s=%q, want blank", id, key, value)
			}
		}
		if len(got) != len(claudeFamilies)*2 {
			t.Errorf("claudeFamilyDefaultEnv(%q) returned %d variables, want %d blanks", id, len(got), len(claudeFamilies)*2)
		}
	}
}

func TestClaudeFamilyDefaultEnvEmptyCatalogue(t *testing.T) {
	if got := claudeFamilyDefaultEnv(nil); got != nil {
		t.Errorf("claudeFamilyDefaultEnv(nil) = %#v, want nil", got)
	}
}

// Both launch paths must carry the overrides: the transparent path reaches Claude Code through Connector but sets the same gateway-mode switch.
func TestClaudeRegistryEntryPinsFamiliesOnBothPaths(t *testing.T) {
	tool, ok := Registry["claude"]
	if !ok {
		t.Fatal("claude is missing from the registry")
	}
	models := modelsFromIDs("claude-opus-5", "claude-sonnet-5")

	injected, err := tool.PrepareWithModels("https://api.everyapi.ai", "sk-relay", models, "claude-opus-5")
	if err != nil {
		t.Fatalf("PrepareWithModels: %v", err)
	}
	if injected["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-5" {
		t.Errorf("injected opus = %q, want claude-opus-5", injected["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}

	transparent, err := tool.PrepareTransparentWithModels(models, "claude-opus-5")
	if err != nil {
		t.Fatalf("PrepareTransparentWithModels: %v", err)
	}
	if transparent["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-5" {
		t.Errorf("transparent opus = %q, want claude-opus-5", transparent["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
	// The transparent path must stay credential-free.
	for key, value := range transparent {
		if value == "sk-relay" {
			t.Errorf("transparent env %s leaked the relay key", key)
		}
	}
}

// The boot model is the picker's selection for this launch and is already applied through --model. Letting it drive the family overrides would pin every alias to one model and defeat switching inside the session.
func TestClaudeFamilyOverridesIgnoreBootModel(t *testing.T) {
	tool := Registry["claude"]
	models := modelsFromIDs("claude-opus-5", "claude-sonnet-5")
	withSonnetBoot, err := tool.PrepareWithModels("https://api.everyapi.ai", "sk-relay", models, "claude-sonnet-5")
	if err != nil {
		t.Fatalf("PrepareWithModels: %v", err)
	}
	if withSonnetBoot["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-5" {
		t.Errorf("opus = %q, want claude-opus-5 even when booting on sonnet", withSonnetBoot["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
}
