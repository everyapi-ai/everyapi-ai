package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// prepareGrok keeps EveryAPI-launched Grok sessions and authentication apart from the user's normal ~/.grok state. The persistent home preserves routed sessions and preferences. Grok 1.0.3 requires an explicit API-key preference for headless ACP launches even when XAI_API_KEY is present.
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
	if err := forceGrokAPIKeyAuth(grokHome); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(grokHome, "auth.json")); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove EveryAPI Grok OAuth cache: %w", err)
	}
	return map[string]string{
		"GROK_HOME": grokHome,
	}, nil
}

func forceGrokAPIKeyAuth(grokHome string) error {
	configPath := filepath.Join(grokHome, "config.toml")
	configMap := make(map[string]any)
	body, err := os.ReadFile(configPath)
	if err == nil {
		if _, err := toml.Decode(string(body), &configMap); err != nil {
			return fmt.Errorf("parse existing Grok config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read Grok config: %w", err)
	}
	auth, exists := configMap["auth"]
	if !exists {
		auth = make(map[string]any)
		configMap["auth"] = auth
	}
	authMap, ok := auth.(map[string]any)
	if !ok {
		return fmt.Errorf("parse existing Grok config: auth must be a table")
	}
	if authMap["preferred_method"] == "api_key" {
		return nil
	}
	authMap["preferred_method"] = "api_key"
	var prepared bytes.Buffer
	if err := toml.NewEncoder(&prepared).Encode(configMap); err != nil {
		return fmt.Errorf("encode Grok config: %w", err)
	}
	if err := writeFileAtomic(configPath, prepared.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write Grok config: %w", err)
	}
	return nil
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
