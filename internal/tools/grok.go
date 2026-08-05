package tools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

// prepareGrok keeps EveryAPI-launched Grok sessions and authentication apart
// from the user's normal ~/.grok state. GROK_MODELS_BASE_URL selects API-key
// routing, while the separate home prevents EveryAPI sessions and preferences
// from mixing with plain Grok launches. The directory is reused so routed
// sessions still persist across launches.
func prepareGrok(_, _ string) (map[string]string, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve everyapi config dir: %w", err)
	}
	grokHome := filepath.Join(cfgDir, "grok-home")
	if err := os.MkdirAll(grokHome, 0o700); err != nil {
		return nil, fmt.Errorf("create grok-home: %w", err)
	}
	return map[string]string{"GROK_HOME": grokHome}, nil
}
