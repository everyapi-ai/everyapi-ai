package tools

import (
	"strconv"
	"strings"
)

// claudeFamily names the pair of variables that override one Claude Code family alias: which model the alias resolves to, and what the resulting picker entry is called.
type claudeFamily struct {
	modelEnv string
	nameEnv  string
}

// claudeFamilies is the set of family aliases Claude Code exposes an override for. It is a map of variable names, not of models: no version, id, or release date appears here, so a model released after this file was written is picked up from the catalogue with no edit. A family Claude Code has no override variable for cannot be steered at all and is deliberately absent.
var claudeFamilies = map[string]claudeFamily{
	"opus":   {modelEnv: "ANTHROPIC_DEFAULT_OPUS_MODEL", nameEnv: "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME"},
	"sonnet": {modelEnv: "ANTHROPIC_DEFAULT_SONNET_MODEL", nameEnv: "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"},
	"haiku":  {modelEnv: "ANTHROPIC_DEFAULT_HAIKU_MODEL", nameEnv: "ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME"},
	"fable":  {modelEnv: "ANTHROPIC_DEFAULT_FABLE_MODEL", nameEnv: "ANTHROPIC_DEFAULT_FABLE_MODEL_NAME"},
}

// claudeVersion is one catalogue id's position within its family. The generation lives in `segments` (claude-opus-4-5 is 4.5); a trailing YYYYMMDD release stamp lives in `build`, separately, because Anthropic writes it in the same dash-separated position a version segment occupies and it is orders of magnitude larger than any real minor version.
type claudeVersion struct {
	segments []int
	build    int
}

// claudeCandidate is the id currently winning a family, kept with its parsed version so the picker label is rendered from the same parse that ranked it.
type claudeCandidate struct {
	id      string
	version claudeVersion
}

// claudeFamilyDefaultEnv pins each Claude Code family alias to the newest id of that family in the launch catalogue.
//
// The "claude" entry sets CLAUDE_CODE_USE_GATEWAY because Claude Code fetches /v1/models only in gateway mode. That same switch moves it off its first-party model table onto the gateway provider tier, whose alias map is a constant compiled into the client: there `opus` resolves to claude-opus-4-7 and `sonnet` to claude-sonnet-4-6 no matter what the catalogue returned. Discovery populates the picker; it never feeds alias resolution. So against a gateway serving only the 5 family, the picker's Default, Opus and Sonnet entries all resolve to ids the relay key cannot route, and the user sees retired models offered as current ones.
//
// ANTHROPIC_DEFAULT_<FAMILY>_MODEL is read ahead of that table, so one variable per family restores the mapping. The values are read off the catalogue rather than written as constants here: pinning today's ids would move the same staleness into EveryAPI and put this list one release behind again the next time a family ships.
//
// Setting the override also removes the retired entries from the picker: Claude Code lists one entry per family built from the override instead of its own tier list, so the Opus 4.6/4.7/4.8 and Sonnet 4.6 rows that the gateway cannot route stop being offered at all. The companion _NAME variable keeps that entry labelled "Opus 5" rather than the raw id.
//
// A family the catalogue lacks has its pair blanked rather than left alone. An empty value reads as unset to Claude Code (verified against the client: an empty ANTHROPIC_DEFAULT_OPUS_MODEL resolves exactly as an absent one), so the alias falls back to Claude Code's own resolution — but an ANTHROPIC_DEFAULT_* export sitting in the user's shell no longer leaks into the launch and points the alias at whatever that machine happened to have configured. The same entry already blanks an ambient ANTHROPIC_API_KEY for this reason.
func claudeFamilyDefaultEnv(models []Model) map[string]string {
	// No catalogue at all is the no-information case (Prepare's model-less path), not a catalogue that happens to serve no Claude family. Blanking on no information would strip a deliberate export for a launch this function knows nothing about.
	if len(models) == 0 {
		return nil
	}
	chosen := claudeFamilyCandidates(models)
	env := make(map[string]string, len(claudeFamilies)*2)
	for family, spec := range claudeFamilies {
		candidate, served := chosen[family]
		if !served {
			env[spec.modelEnv] = ""
			env[spec.nameEnv] = ""
			continue
		}
		env[spec.modelEnv] = candidate.id
		env[spec.nameEnv] = claudeFamilyDisplayName(family, candidate.version)
	}
	return env
}

// claudeContextMarker1M is the marker Claude Code reads to select Anthropic's 1M-context beta. It is the client's own notation, not a routable model id: the client validates a model by stripping this suffix, then strips it again before the id goes on the wire, and the only thing the marker leaves behind is `anthropic-beta: context-1m-2025-08-07` on the request. The gateway re-appends it to the model name it records (relay/channel/claude's preserveClaudeCodeContextModel) so a resumed transcript keeps its prompt cache. Nothing downstream ever has to route an id carrying it.
const claudeContextMarker1M = "[1m]"

// ClaudeBootModelWithContextMarker returns the id Claude Code should boot on for a catalogue id, marking the Opus family for 1M context.
//
// Without the marker an EveryAPI launch is strictly worse than a bare `claude`: the boot model reaches the client through --model as a plain id, so the client never asks for the beta and the session runs at 200K where the same model under a bare `claude` would not. The user's only way back was to reopen /model inside the session and pick Claude Code's own "Opus (1M context)" row, a session-scoped choice that claude_user_settings.go then correctly reverts on exit.
//
// Opus and nothing else. sonnet and haiku are rejected outright by Anthropic for this flag, so marking them would break a launch rather than widen it; claudeBetaTierFlags in backend/internal/relay/channel/claude/cli_envelope.go draws the same tier line for the envelope it synthesises. There is deliberately no generation floor: the repository has no per-generation long-context table to read one out of, and inventing a cutoff here would be a guess that silently ages.
//
// The caller decides WHETHER to mark at all — see config.Settings.ClaudeLongContextEnabled. The beta is account-gated upstream and the gateway forwards the client's anthropic-beta header verbatim, so on a relay key whose Anthropic channel lacks long context every request in the session is rejected rather than merely running short. This function only answers "which id would carry it".
//
// An id that already carries the marker is returned unchanged, so this stays safe to apply to a value that has been through it before.
func ClaudeBootModelWithContextMarker(id string) string {
	if id == "" || strings.HasSuffix(id, claudeContextMarker1M) {
		return id
	}
	family, catalogueID, _, ok := parseClaudeModelID(id)
	if !ok || family != "opus" {
		return id
	}
	// The parsed (trimmed) id, not the raw argument: cliout.Sanitize strips control bytes from a catalogue id without trimming spaces — the shape claudeCatalogModels already trims on lookup — and appending the marker to " claude-opus-5 " would hand Claude Code "claude-opus-5 [1m]", whose `\[1m\]$` strip leaves a spaced id the gateway cannot route.
	return catalogueID + claudeContextMarker1M
}

// ClaudeCatalogueID reverses ClaudeBootModelWithContextMarker back to the catalogue id it was applied to. The published catalogue never carries the marker, so anything keyed on a catalogue id — ClaudeFamilyAliasedModelIDs' withheld set and the launch catalogue's by-id lookup above all — must compare against this rather than against the boot model.
func ClaudeCatalogueID(bootModel string) string {
	return strings.TrimSuffix(bootModel, claudeContextMarker1M)
}

// claudeFamilyCandidates resolves the newest catalogue id for each family Claude Code exposes an override for. Families the catalogue does not serve are absent from the result rather than present with a zero value, so a caller can tell "no id won this family" from "this family is not tracked".
func claudeFamilyCandidates(models []Model) map[string]claudeCandidate {
	chosen := make(map[string]claudeCandidate, len(claudeFamilies))
	for _, model := range models {
		family, id, version, ok := parseClaudeModelID(model.ID)
		if !ok {
			continue
		}
		if _, tracked := claudeFamilies[family]; !tracked {
			continue
		}
		if current, seen := chosen[family]; seen && compareClaudeVersions(version, current.version) <= 0 {
			continue
		}
		chosen[family] = claudeCandidate{id: id, version: version}
	}
	return chosen
}

// ClaudeFamilyAliasedModelIDs is the set of catalogue ids that claudeFamilyDefaultEnv has already turned into a Claude Code family row — the ids behind the picker's "Opus 5" / "Sonnet 5" / "Haiku 4.5" / "Fable 5" entries for this launch.
//
// It exists so the published catalogue can leave those ids out. Claude Code builds the picker from two independent sources: one row per family from the ANTHROPIC_DEFAULT_<FAMILY>_MODEL overrides, plus a row per entry discovered through GET /v1/models. It does not reconcile the two, so an id that is both the family default and a discovered entry is offered twice — once labelled "Opus 5", once as the raw id — and the two rows launch the identical model.
//
// Only the winning id per family is in the set. An older sibling the same catalogue still carries (claude-opus-4-5 next to claude-opus-5) never became a family row, so it has to stay discoverable or it would become unreachable from the picker entirely. Ids parseClaudeModelID rejects — suffixed variants, legacy claude-3-5-* shapes, marketplace listings that merely name themselves claude-* — are likewise absent, which is what keeps them published.
func ClaudeFamilyAliasedModelIDs(models []Model) map[string]bool {
	// Mirrors claudeFamilyDefaultEnv's own guard: with no catalogue there are no overrides, so nothing has been aliased and nothing should be withheld.
	if len(models) == 0 {
		return nil
	}
	chosen := claudeFamilyCandidates(models)
	aliased := make(map[string]bool, len(chosen))
	for _, candidate := range chosen {
		aliased[candidate.id] = true
	}
	return aliased
}

// claudeFamilyDisplayName builds the picker label out of the id itself — the capitalised family plus a dotted generation, so claude-haiku-4-5 reads as "Haiku 4.5". Deriving it means a model this code has never seen still gets a correct label. The release stamp is deliberately absent: claude-haiku-4-5-20251001 is still Haiku 4.5 to the reader, and spelling it "Haiku 4.5.20251001" names a build nobody chose by date. family is non-empty: only a key present in claudeFamilies reaches here.
func claudeFamilyDisplayName(family string, version claudeVersion) string {
	segments := make([]string, 0, len(version.segments))
	for _, segment := range version.segments {
		segments = append(segments, strconv.Itoa(segment))
	}
	return strings.ToUpper(family[:1]) + family[1:] + " " + strings.Join(segments, ".")
}

// parseClaudeModelID splits claude-<family>-<numeric version segments>[-YYYYMMDD] into its family, its catalogue id, and a comparable version. A suffixed variant like claude-opus-4-7-thinking or claude-opus-5[1m], and a legacy id whose family is not in the third position at all (claude-3-5-sonnet), report false so they cannot become a family default.
//
// It cannot tell an EveryAPI-served Anthropic model from a marketplace listing that merely names itself claude-*: /v1/models derives owned_by from the model id itself (persistence.ModelOwnedBy), so every claude-* entry reports "anthropic" whatever backs it. The id shape is the only signal available here, and it is the same signal the picker itself shows the user.
func parseClaudeModelID(id string) (string, string, claudeVersion, bool) {
	trimmed := strings.TrimSpace(id)
	rest, found := strings.CutPrefix(strings.ToLower(trimmed), "claude-")
	if !found {
		return "", "", claudeVersion{}, false
	}
	parts := strings.Split(rest, "-")
	if len(parts) < 2 {
		return "", "", claudeVersion{}, false
	}
	segments := parts[1:]
	var version claudeVersion
	if build, dated := claudeBuildStamp(segments[len(segments)-1]); dated {
		version.build = build
		segments = segments[:len(segments)-1]
	}
	if len(segments) == 0 {
		return "", "", claudeVersion{}, false
	}
	version.segments = make([]int, 0, len(segments))
	for _, part := range segments {
		segment, err := strconv.Atoi(part)
		if err != nil {
			return "", "", claudeVersion{}, false
		}
		version.segments = append(version.segments, segment)
	}
	return parts[0], trimmed, version, true
}

// claudeBuildStamp reads a trailing YYYYMMDD release stamp off the last dash-separated segment. Splitting it out of the version is load-bearing rather than cosmetic: Anthropic publishes claude-opus-4-20250514 for Opus 4 and claude-opus-4-6 for Opus 4.6, both gateway-facing ids in modelcaps' catalogue, so comparing the date as if it were a minor version makes the May 2025 build outrank every 4.x release that followed it.
func claudeBuildStamp(part string) (int, bool) {
	if len(part) != 8 {
		return 0, false
	}
	stamp, err := strconv.Atoi(part)
	if err != nil || stamp < 19700101 {
		return 0, false
	}
	return stamp, true
}

// compareClaudeVersions orders two ids of the same family: generation first, numerically, so 5 outranks 4-8 and 4-10 outranks 4-9.
//
// Within one generation the undated rolling id wins. It is the alias Anthropic keeps pointed at the current build, so pinning the picker to a snapshot would strand the family the day that snapshot retires, and it labels the entry "Haiku 4.5" instead of naming a build date. Between two snapshots the newer date wins, so a Bedrock/Vertex-only key — which never sees the rolling id at all — still gets its newest build.
func compareClaudeVersions(a, b claudeVersion) int {
	for i := 0; i < len(a.segments) && i < len(b.segments); i++ {
		if a.segments[i] != b.segments[i] {
			if a.segments[i] < b.segments[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.segments) < len(b.segments):
		return -1
	case len(a.segments) > len(b.segments):
		return 1
	case a.build == b.build:
		return 0
	case a.build == 0:
		return 1
	case b.build == 0:
		return -1
	case a.build < b.build:
		return -1
	}
	return 1
}

// prepareClaudeWithModels supplies the family overrides on the injected path. It needs neither the base URL nor the relay key: the overrides carry model ids only.
func prepareClaudeWithModels(_, _ string, models []Model) (map[string]string, error) {
	return claudeFamilyDefaultEnv(models), nil
}

// prepareClaudeTransparentWithModels is the transparent counterpart. Model ids are public routing information, so the same overrides are safe on a path that must keep the relay key inside the connector process.
func prepareClaudeTransparentWithModels(models []Model, _ string) (map[string]string, error) {
	return claudeFamilyDefaultEnv(models), nil
}
