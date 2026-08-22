package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

// ReasoningLevelEnv carries the reasoning level `everyapi use` resolved for this launch to the tool's prepare hook. It is the same in-process channel Tool.ModelEnv uses for the model, and for the same reason: the hooks run after the picker, inside the tools package, and threading a second selection through every prepare signature would touch every client that has no level control at all. Unset or empty means "leave the tool on its own default".
//
// Because it travels as a real environment variable, it also reaches the launched tool's own children — a nested `everyapi use` inside a Codex or pi session inherits whatever the outer launch exported. The levels are per-client vocabularies ("off" means something to pi and nothing to codex), so an inherited value must never be forwarded. resolveReasoningLevel is therefore the sole author of this variable and clears it on every path that does not resolve a level of its own.
const ReasoningLevelEnv = "EVERYAPI_REASONING_LEVEL"

// piThinkingSupport is the single source for both the EveryAPI launcher's level picker and the thinkingLevelMap written into Pi's model catalogue. A nil levelMap uses Pi's standard provider mapping.
type piThinkingSupport struct {
	levels   []string
	levelMap map[string]any
}

// piStandardThinkingLevels are Pi's non-extended defaults. The extended pair is opt-in: Pi hides xhigh/max unless thinkingLevelMap names provider values for them.
var piStandardThinkingLevels = []string{"off", "minimal", "low", "medium", "high"}

// Luna's Responses API rejects "minimal" and explicitly accepts none, low, medium, high, xhigh, and max. Keep the unsupported Pi level as null so Shift+Tab skips it instead of sending a request that the upstream rejects with 400.
var piLunaThinkingSupport = piThinkingSupport{
	levels: []string{"off", "low", "medium", "high", "xhigh", "max"},
	levelMap: map[string]any{
		"off": "none", "minimal": nil, "low": "low", "medium": "medium",
		"high": "high", "xhigh": "xhigh", "max": "max",
	},
}

func piThinkingSupportForModel(model Model) piThinkingSupport {
	if model.ID == "gpt-5.6-luna" {
		return piLunaThinkingSupport
	}
	return piThinkingSupport{levels: piStandardThinkingLevels}
}

// piDefaultThinkingLevel is where the picker's cursor starts for a first launch, and it is EveryAPI's own choice rather than a value read back from pi. Unlike codex — whose bundled catalogue publishes a default_reasoning_level per slug — pi ships no per-model default for a custom provider, so there is no "what the tool would have done by itself" to land on. The middle of the offered range is the least surprising stand-in: it matches what the reasoning models themselves apply when no effort is sent.
const piDefaultThinkingLevel = "medium"

// reasoningLevelClients is the single registry of clients that have a level control, so ReasoningLevels and SupportsReasoningLevels cannot disagree about which those are. A client absent from this map has no level step at all and is never prompted.
var reasoningLevelClients = map[string]func(Model) ([]string, string){
	"codex": func(model Model) ([]string, string) { return codexReasoningLevels(model.ID) },
	"pi":    piReasoningLevels,
}

// SupportsReasoningLevels reports whether this client has a reasoning-level control at all, without consulting a model. It answers the cheap half of ReasoningLevels' question, so a launch of a client that has no such control can skip the catalogue lookup the model half needs.
func SupportsReasoningLevels(t *Tool) bool {
	if t == nil {
		return false
	}
	_, ok := reasoningLevelClients[t.Name]
	return ok
}

// ReasoningLevels returns the levels this tool can launch the given model at, in the tool's own vocabulary and ordered as the tool orders them, plus the level the tool would use on its own. An empty list means the pairing has no level control — either the client has none, or nothing has verified that this model takes an effort — and callers must skip the prompt rather than offer a default.
//
// The two tools answer from different sources on purpose. Codex ships its own model catalogue and states per slug which efforts it accepts (gpt-5.6-sol reaches ultra, gpt-5.5 stops at xhigh), so asking it is both exact and free of guesswork. Pi has no such per-model table for a custom provider — the levels come from whatever the generated models.json declares — so the gateway's supports_thinking is what decides whether the control appears at all.
func ReasoningLevels(t *Tool, model Model) (levels []string, preferred string) {
	if t == nil || strings.TrimSpace(model.ID) == "" {
		return nil, ""
	}
	resolve, ok := reasoningLevelClients[t.Name]
	if !ok {
		return nil, ""
	}
	return resolve(model)
}

// PersistedReasoningLevel reports the level this client last recorded outside the launcher, or "" when it keeps none. It is what the tool would boot at if `everyapi use` asked nothing, so it — not the vendor's per-model default — is where an interactive picker's cursor belongs on the first launch after this feature ships.
//
// Only Codex has one. Its EveryAPI-owned home survives across launches and inheritPersistentCodexReasoningEffort feeds it into each fresh lifecycle-bound profile, so a user who set "high" there before ever seeing this picker must be shown "high". Pi's agent dir is process-scoped and deleted on exit, so there is nothing to read back.
func PersistedReasoningLevel(t *Tool) string {
	if t == nil || t.Name != "codex" {
		return ""
	}
	configDir, err := config.ConfigDir()
	if err != nil {
		return ""
	}
	defaults, err := readCodexUserDefaults(filepath.Join(configDir, "codex-home"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(defaults.ModelReasoningEffort)
}

// selectedReasoningLevel reads the level the launcher resolved for this process.
func selectedReasoningLevel() string {
	return strings.TrimSpace(os.Getenv(ReasoningLevelEnv))
}

// piReasoningLevels offers pi's scale only where the gateway has verified the model takes an effort. supports_thinking = false means unknown, not refused, and declaring a control for a model of unknown shape would send reasoning_effort upstream and 400.
func piReasoningLevels(model Model) ([]string, string) {
	if !model.SupportsThinking {
		return nil, ""
	}
	support := piThinkingSupportForModel(model)
	return append([]string(nil), support.levels...), piDefaultThinkingLevel
}

// codexReasoningLevels reports the efforts Codex's bundled catalogue publishes for a slug. A model EveryAPI routes but Codex has never heard of returns nil, which matches what writeCodexModelCatalog writes for it: an entry with supported_reasoning_levels emptied, so Codex's own picker offers no effort step either.
//
// Errors are swallowed into nil rather than propagated. This runs to decide whether to ask one extra question; a Codex that cannot be executed yet, or is too old for `debug models --bundled`, must not turn that into a failed launch. The same call inside writeCodexModelCatalog does surface its error, because there the catalogue is the thing being written.
func codexReasoningLevels(modelID string) ([]string, string) {
	body, err := codexBundledCatalog()
	if err != nil {
		return nil, ""
	}
	var bundled struct {
		Models []struct {
			Slug                     string `json:"slug"`
			DefaultReasoningLevel    string `json:"default_reasoning_level"`
			SupportedReasoningLevels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &bundled); err != nil {
		return nil, ""
	}
	for _, model := range bundled.Models {
		if model.Slug != modelID {
			continue
		}
		levels := make([]string, 0, len(model.SupportedReasoningLevels))
		for _, level := range model.SupportedReasoningLevels {
			if effort := strings.TrimSpace(level.Effort); effort != "" {
				levels = append(levels, effort)
			}
		}
		if len(levels) == 0 {
			return nil, ""
		}
		return levels, strings.TrimSpace(model.DefaultReasoningLevel)
	}
	return nil, ""
}
