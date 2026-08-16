package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const forgeModelEnv = "EVERYAPI_FORGE_MODEL"

// prepareForgeWithModels places Forge's global configuration in a lifecycle-bound home. This also contains any credential migration Forge may perform, so the relay key cannot escape into the user's persistent profile.
func prepareForgeWithModels(_ string, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(forgeModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", forgeModelEnv)
	}
	for _, model := range models {
		if strings.TrimSpace(model.ID) != selected {
			continue
		}
		providerID := "openai_compatible"
		if !modelSupportsEndpoint(model.SupportedEndpointTypes, "openai") &&
			modelSupportsEndpoint(model.SupportedEndpointTypes, "openai-response") {
			providerID = "openai_responses_compatible"
		}
		home, err := newPreparedHome("forge")
		if err != nil {
			return nil, err
		}
		config := struct {
			Session struct {
				ProviderID string `toml:"provider_id"`
				ModelID    string `toml:"model_id"`
			} `toml:"session"`
		}{}
		config.Session.ProviderID = providerID
		config.Session.ModelID = selected
		var body strings.Builder
		if err := toml.NewEncoder(&body).Encode(config); err != nil {
			removePreparedHomeAfterQuiet(home)
			return nil, fmt.Errorf("encode Forge runtime settings: %w", err)
		}
		if err := writeFileAtomic(filepath.Join(home, ".forge.toml"), []byte(body.String()), 0o600); err != nil {
			removePreparedHomeAfterQuiet(home)
			return nil, err
		}
		return map[string]string{
			preparedHomeMarker:           home,
			"FORGE_CONFIG":               home,
			"FORGE_SESSION__PROVIDER_ID": providerID,
			"FORGE_SESSION__MODEL_ID":    selected,
		}, nil
	}
	return nil, fmt.Errorf("%s=%q is absent from the launch catalog", forgeModelEnv, selected)
}
