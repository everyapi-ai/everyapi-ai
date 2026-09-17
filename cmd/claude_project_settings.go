package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

// claudeProjectSettingsFiles are the settings files Claude Code loads from the working directory, in descending precedence. Both outrank the user's own ~/.claude/settings.json, which is the whole reason anything in this file exists: an EveryAPI launch reaches Claude Code through argv, and argv outranks every one of them.
var claudeProjectSettingsFiles = []string{"settings.local.json", "settings.json"}

// claudeProjectSettingsClaim asks each project settings file in precedence order whether it claims ownership of something this launch would otherwise inject, and reports the first yes.
//
// Unreadable and unparsable files answer yes. A file this cannot read is a file whose contents it must not overrule, and Claude Code's own parser owns validation and diagnostics for a malformed one — masking that by proceeding with an injected flag would report the wrong problem.
//
// Never move CLAUDE_CONFIG_DIR or the working directory to influence this. The former stores user skills and transcripts, not the project settings discovery root.
//
// The working directory, and no parent of it. This looks like a gap — a repo whose root carries the policy, entered from a subdirectory, claims nothing — and it is not one: Claude Code's own project settings discovery is cwd-only, verified against 2.1.274 by putting a `model` a catalogue cannot resolve in a root .claude/settings.json and launching from both places. From the root the launch fails on that model; from a subdirectory the same file has no effect whatsoever. Walking up here would therefore yield the boot model to a file Claude Code is never going to read, and the session would fall through to ~/.claude/settings.json — the global default this whole file exists to keep out of a project's way. Match the client's discovery; do not widen it.
func claudeProjectSettingsClaim(claims func(map[string]json.RawMessage) bool) bool {
	for _, name := range claudeProjectSettingsFiles {
		body, err := os.ReadFile(filepath.Join(".claude", name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return true
		}
		var settings map[string]json.RawMessage
		if json.Unmarshal(body, &settings) != nil {
			return true
		}
		if claims(settings) {
			return true
		}
	}
	return false
}

// claudeOwnsBootModel prevents EveryAPI's globally remembered model from
// becoming a higher-priority --model override of a project or resumed session.
// Do not merge/re-emit settings through --settings: that would promote user
// settings above project/local settings, including permissions and hooks.
// Claude keeps ownership of scope, trust, validation and session persistence.
func claudeOwnsBootModel(t *tools.Tool, args []string, model string, pick bool) bool {
	if t == nil || t.Name != "claude" || model != "" || pick {
		return false
	}
	if !toolInvocationNeedsEndpoint(args) {
		return true
	}
	for _, flag := range []string{"--model", "--settings", "--setting-sources", "--resume", "-r", "--continue", "-c"} {
		if containsFlag(args, flag) {
			return true
		}
	}
	return claudeProjectSettingsClaim(claudeSettingsSelectModel)
}

// claudeSettingsSelectModel reports whether one settings file chooses the model, by either route Claude Code offers.
//
// The top-level `model` field is the obvious one. The `env` block is the one that used to be missed: setting ANTHROPIC_MODEL there names a model outright, and setting a family variable redirects what `opus` or `sonnet` resolves to. Both are as deliberate as the field, and both lose to an injected --model, since argv outranks anything a settings file can put in the environment. A project that picked its model through the env block was therefore silently launched on EveryAPI's globally remembered one instead.
func claudeSettingsSelectModel(settings map[string]json.RawMessage) bool {
	if raw, present := settings["model"]; present && claudeSettingsValueNamesModel(raw) {
		return true
	}
	raw, present := settings["env"]
	if !present {
		return false
	}
	var block map[string]json.RawMessage
	if json.Unmarshal(raw, &block) != nil {
		return true // an `env` that is not an object is Claude's to reject, not this launch's to reinterpret
	}
	for _, key := range tools.ClaudeModelSelectionEnvKeys() {
		if value, set := block[key]; set && claudeSettingsValueNamesModel(value) {
			return true
		}
	}
	return false
}

// claudeSettingsValueNamesModel reports whether a settings value actually names a model. An empty string does not: Claude Code reads a blank ANTHROPIC_DEFAULT_OPUS_MODEL exactly as an absent one, which is the same equivalence claudeFamilyDefaultEnv relies on when it blanks a family the catalogue does not serve. A non-string value counts, because deciding what it means is Claude Code's job and yielding is the safe direction.
func claudeSettingsValueNamesModel(raw json.RawMessage) bool {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return true
	}
	return strings.TrimSpace(text) != ""
}

// claudeOwnsPermissionMode reports whether this launch must leave Claude Code's permission mode to the project rather than injecting the dangerous-mode flag.
//
// EveryAPI's dangerous-mode preference is stored once, globally, in its own settings — so a single yes, given in whatever directory the user happened to be in, prepended --dangerously-skip-permissions to every Claude launch in every project afterwards. That flag does not merge with a project's rules, it bypasses them: the permissions block in .claude/settings.json stops being consulted at all. A globally remembered convenience silently disarming a per-project safety boundary is the same inversion claudeOwnsBootModel exists to prevent, one file over.
//
// So a project that states a permissions policy keeps it. The user can still bypass it for one launch by passing the flag explicitly after `--`, which is deliberate: that is a per-invocation decision made in the project, not a preference carried in from somewhere else. An explicit --permission-mode is honoured the same way — it is the user naming a mode, and injecting a flag that overrides the mode they just named would make the argument they typed do nothing.
func claudeOwnsPermissionMode(t *tools.Tool, args []string) bool {
	if t == nil || t.Name != "claude" {
		return false
	}
	if containsFlag(args, claudePermissionModeFlag) {
		return true
	}
	return claudeProjectSettingsClaim(claudeSettingsSetPermissions)
}

// claudePermissionModeFlag is the argument through which the user names the permission mode themselves. Ownership is the same either way, but only the settings route is worth announcing: a user who just typed a mode on the command line does not need to be told why the bypass was not added, and the announcement would credit a settings file that may not exist.
const claudePermissionModeFlag = "--permission-mode"

// announceProjectOwnedPermissionMode reports whether a launch that left the permission mode to the project should say so. The notice exists to explain a missing flag, so it is only worth printing when this launch would otherwise have produced one: an explicit --permission-mode is the user's own doing and explains itself, and a dangerous-mode preference that is saved as off — or unset with no TTY to ask on — was never going to add anything to withhold.
func announceProjectOwnedPermissionMode(args []string, dangerousMode *bool, interactive bool) bool {
	if containsFlag(args, claudePermissionModeFlag) {
		return false
	}
	if dangerousMode != nil {
		return *dangerousMode
	}
	return interactive
}

// claudeSettingsSetPermissions reports whether one settings file states a permissions policy. An absent block is no policy; so is an empty one, which states nothing that a bypass could override.
//
// Any other non-empty block counts, without inspecting which keys it holds. A block carrying only additionalDirectories does widen rather than restrict, so treating it as a policy withholds the bypass from a project that never asked for that — a small, visible, per-launch cost the printed notice explains. The alternative is a hand-maintained list of which permission keys are restrictive, and the day Claude Code adds one that this list has not learned yet, a real policy silently stops being honoured. Between erring toward asking and erring toward bypassing someone's rules, only one of those is recoverable by typing a flag.
func claudeSettingsSetPermissions(settings map[string]json.RawMessage) bool {
	raw, present := settings["permissions"]
	if !present {
		return false
	}
	var block map[string]json.RawMessage
	if json.Unmarshal(raw, &block) != nil {
		return true // same rule as everywhere else here: a file this cannot read is one it must not overrule
	}
	return len(block) > 0
}
