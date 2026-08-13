package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// prepareGrok keeps EveryAPI-launched Grok sessions and authentication apart
// from the user's normal ~/.grok state. The persistent home preserves routed
// sessions and preferences, while a process-scoped auth path prevents a cached
// xAI browser session from taking precedence over EveryAPI's XAI_API_KEY.
func prepareGrok(_, _ string) (map[string]string, error) {
	return prepareGrokWithModels("", "", nil)
}

func prepareGrokWithModels(_, _ string, models []Model) (map[string]string, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve everyapi config dir: %w", err)
	}
	grokHome := filepath.Join(cfgDir, "grok-home")
	if err := os.MkdirAll(grokHome, 0o700); err != nil {
		return nil, fmt.Errorf("create grok-home: %w", err)
	}
	if err := rejectGrokModelOverrides(grokHome, models); err != nil {
		return nil, err
	}
	authHome, err := newPreparedHome("grok-auth")
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"GROK_HOME":        grokHome,
		"GROK_AUTH_PATH":   filepath.Join(authHome, "auth.json"),
		preparedHomeMarker: authHome,
	}, nil
}

func rejectGrokModelOverrides(grokHome string, models []Model) error {
	body, err := os.ReadFile(filepath.Join(grokHome, "config.toml"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Grok config: %w", err)
	}
	var configMap struct {
		Models map[string]any `toml:"model"`
	}
	if _, err := toml.Decode(string(body), &configMap); err != nil {
		return fmt.Errorf("parse existing Grok config: %w", err)
	}
	conflicts := make([]string, 0)
	for _, model := range models {
		if _, exists := configMap.Models[model.ID]; exists {
			conflicts = append(conflicts, model.ID)
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	sort.Strings(conflicts)
	return fmt.Errorf("Grok config overrides EveryAPI model routing for %s; rename or remove the matching [model.*] entry", strings.Join(conflicts, ", "))
}
