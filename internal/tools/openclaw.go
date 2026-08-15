package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func encodeOpenCodeCompatibleConfig(apiBase, selected string, models []Model) (string, error) {
	providerModels := make(map[string]openCodeModel, len(models))
	for _, model := range models {
		name := model.DisplayName
		if name == "" {
			name = model.ID
		}
		providerModels[model.ID] = openCodeModel{Name: name}
	}
	config := openCodeConfig{
		Schema: "https://opencode.ai/config.json",
		Provider: map[string]openCodeProvider{"everyapi": {
			NPM: "@ai-sdk/openai-compatible", Name: "EveryAPI",
			Options: openCodeProviderOptions{BaseURL: joinBase(apiBase, "/v1"), APIKey: "{env:" + openCodeCredentialEnv + "}"},
			Models:  providerModels,
		}},
		Model: "everyapi/" + selected,
	}
	body, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode OpenCode-compatible config: %w", err)
	}
	return string(body), nil
}

const (
	openClawModelEnv      = "EVERYAPI_OPENCLAW_MODEL"
	openClawCredentialEnv = "EVERYAPI_RELAY_KEY"
)

func prepareOpenClawWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(openClawModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", openClawModelEnv)
	}
	home, err := newPreparedHome("openclaw")
	if err != nil {
		return nil, err
	}
	providerModels := make([]map[string]any, 0, len(models))
	modelAliases := make(map[string]any, len(models))
	for _, model := range models {
		name := model.DisplayName
		if name == "" {
			name = model.ID
		}
		providerModels = append(providerModels, map[string]any{"id": model.ID, "name": name})
		modelAliases["everyapi/"+model.ID] = map[string]any{}
	}
	config := map[string]any{
		"models": map[string]any{"providers": map[string]any{"everyapi": map[string]any{
			"baseUrl": joinBase(apiBase, "/v1"), "api": "openai-completions",
			"apiKey": map[string]any{"source": "env", "provider": "default", "id": openClawCredentialEnv},
			"models": providerModels,
		}}},
		"agents": map[string]any{"defaults": map[string]any{
			"model": map[string]any{"primary": "everyapi/" + selected}, "models": modelAliases,
		}},
	}
	body, err := json.Marshal(config)
	if err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, fmt.Errorf("encode OpenClaw config: %w", err)
	}
	path := filepath.Join(home, "openclaw.json")
	if err := writeFileAtomic(path, body, 0o600); err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, err
	}
	env := preparedHomeEnv("OPENCLAW_STATE_DIR", home)
	env["OPENCLAW_CONFIG_PATH"] = path
	return env, nil
}
