package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// prepareKimiCode keeps EveryAPI-routed Kimi Code sessions and OAuth state in
// a dedicated home. The KIMI_MODEL_* variables supply an in-memory provider,
// so no relay credential or generated provider config needs to be written.
func prepareKimiCode(_, _ string) (map[string]string, error) {
	return prepareKimiCodeWithModels("", "", nil)
}

func prepareKimiCodeWithModels(_, _ string, models []Model) (map[string]string, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve everyapi config dir: %w", err)
	}
	kimiHome := filepath.Join(cfgDir, "kimi-code-home")
	if len(models) > 0 {
		kimiHome, err = newPreparedHome("kimi")
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(kimiHome, 0o700); err != nil {
		return nil, fmt.Errorf("create kimi-code-home: %w", err)
	}
	if len(models) > 0 {
		if err := writeKimiModelCatalog(kimiHome, models); err != nil {
			removePreparedHomeAfterQuiet(kimiHome)
			return nil, err
		}
	}
	if len(models) > 0 {
		return preparedHomeEnv("KIMI_CODE_HOME", kimiHome), nil
	}
	return map[string]string{"KIMI_CODE_HOME": kimiHome}, nil
}

func writeKimiModelCatalog(kimiHome string, models []Model) error {
	path := filepath.Join(kimiHome, "config.toml")
	configMap := map[string]any{}
	if body, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(body), &configMap); err != nil {
			return fmt.Errorf("parse existing Kimi config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing Kimi config: %w", err)
	}

	aliases, _ := configMap["models"].(map[string]any)
	if aliases == nil {
		aliases = map[string]any{}
	}
	for alias, raw := range aliases {
		entry, _ := raw.(map[string]any)
		if entry != nil && entry["provider"] == "__kimi_env__" {
			delete(aliases, alias)
		}
	}
	for _, model := range models {
		aliases[model.ID] = map[string]any{
			"provider":         "__kimi_env__",
			"model":            model.ID,
			"display_name":     model.ID,
			"max_context_size": int64(128000),
		}
	}
	configMap["models"] = aliases
	var body strings.Builder
	if err := toml.NewEncoder(&body).Encode(configMap); err != nil {
		return fmt.Errorf("encode Kimi config: %w", err)
	}
	return writeFileAtomic(path, []byte(body.String()), 0o600)
}
