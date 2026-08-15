package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	crushModelEnv      = "EVERYAPI_CRUSH_MODEL"
	crushCredentialEnv = "EVERYAPI_RELAY_KEY"
)

func prepareCrushWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(crushModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", crushModelEnv)
	}
	home, err := newPreparedHome("crush")
	if err != nil {
		return nil, err
	}
	providerModels := make([]map[string]any, 0, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model.DisplayName)
		if name == "" {
			name = model.ID
		}
		providerModels = append(providerModels, map[string]any{"id": model.ID, "name": name})
	}
	config := map[string]any{
		"providers": map[string]any{"everyapi": map[string]any{
			"type": "openai-compat", "base_url": joinBase(apiBase, "/v1"),
			"api_key": "$" + crushCredentialEnv, "models": providerModels,
		}},
		"models": map[string]any{"large": map[string]any{"provider": "everyapi", "model": selected}},
	}
	body, err := json.Marshal(config)
	if err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, fmt.Errorf("encode Crush config: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(home, "crush.json"), body, 0o600); err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, err
	}
	return preparedHomeEnv("CRUSH_GLOBAL_CONFIG", home), nil
}
