package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

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
	// Never move CLAUDE_CONFIG_DIR or the working directory. The former stores
	// user skills and transcripts, not the project settings discovery root.
	for _, name := range []string{"settings.local.json", "settings.json"} {
		body, err := os.ReadFile(filepath.Join(".claude", name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return true // let Claude report unreadable settings, not mask them
		}
		var settings map[string]json.RawMessage
		if json.Unmarshal(body, &settings) != nil {
			return true // native parser owns validation and diagnostics
		}
		if _, present := settings["model"]; present {
			return true
		}
	}
	return false
}
