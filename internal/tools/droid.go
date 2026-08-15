package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const droidModelEnv = "EVERYAPI_DROID_MODEL"

func prepareDroidWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(droidModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", droidModelEnv)
	}
	provider := "generic-chat-completion-api"
	for _, model := range models {
		if strings.TrimSpace(model.ID) != selected {
			continue
		}
		if modelSupportsEndpoint(model.SupportedEndpointTypes, "openai-response") {
			provider = "openai"
		}
		home, err := newPreparedHome("droid")
		if err != nil {
			return nil, err
		}
		path := filepath.Join(home, "settings.json")
		settings := map[string]any{
			"model": "custom:EveryAPI-0",
			"customModels": []map[string]any{{
				"model": selected, "displayName": "EveryAPI",
				"baseUrl": joinBase(apiBase, "/v1"), "apiKey": "${EVERYAPI_RELAY_KEY}",
				"provider": provider,
			}},
		}
		body, err := json.Marshal(settings)
		if err != nil {
			removePreparedHomeAfterQuiet(home)
			return nil, fmt.Errorf("encode Droid runtime settings: %w", err)
		}
		if err := writeFileAtomic(path, body, 0o600); err != nil {
			removePreparedHomeAfterQuiet(home)
			return nil, err
		}
		env := map[string]string{preparedHomeMarker: home, preparedArgsMarker: path}
		return env, nil
	}
	return nil, fmt.Errorf("%s=%q is absent from the launch catalog", droidModelEnv, selected)
}
