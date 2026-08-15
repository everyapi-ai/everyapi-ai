package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	clineModelEnv            = "EVERYAPI_CLINE_MODEL"
	clineChatProviderID      = "lmstudio"
	clineResponsesProviderID = "openai-native"
)

type clineProviderSettings struct {
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
	BaseURL  string `json:"baseUrl"`
}

type clineProviderProfile struct {
	Settings    clineProviderSettings `json:"settings"`
	UpdatedAt   string                `json:"updatedAt"`
	TokenSource string                `json:"tokenSource"`
}

type clineProviderSettingsFile struct {
	Version          int                             `json:"version"`
	LastUsedProvider string                          `json:"lastUsedProvider"`
	Providers        map[string]clineProviderProfile `json:"providers"`
}

type clineCatalogProviderInfo struct {
	Name           string   `json:"name"`
	BaseURL        string   `json:"baseUrl"`
	DefaultModelID string   `json:"defaultModelId"`
	Protocol       string   `json:"protocol"`
	Client         string   `json:"client"`
	Capabilities   []string `json:"capabilities"`
}

type clineCatalogModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type clineCatalogProvider struct {
	Provider clineCatalogProviderInfo     `json:"provider"`
	Models   map[string]clineCatalogModel `json:"models"`
}

type clineModelCatalogFile struct {
	Version   int                             `json:"version"`
	Providers map[string]clineCatalogProvider `json:"providers"`
}

func prepareClineWithModels(apiBase, token string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(clineModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", clineModelEnv)
	}
	var chatModels, responsesModels []Model
	selectedProvider := ""
	for _, model := range models {
		if modelSupportsEndpoint(model.SupportedEndpointTypes, "openai") {
			chatModels = append(chatModels, model)
			if model.ID == selected {
				selectedProvider = clineChatProviderID
			}
		}
		if modelSupportsEndpoint(model.SupportedEndpointTypes, "openai-response") {
			responsesModels = append(responsesModels, model)
			if model.ID == selected && selectedProvider == "" {
				selectedProvider = clineResponsesProviderID
			}
		}
	}
	if selectedProvider == "" {
		return nil, fmt.Errorf("Cline model %q is absent from the compatible live catalog", selected)
	}
	home, err := newPreparedHome("cline")
	if err != nil {
		return nil, err
	}
	settingsDir := filepath.Join(home, "settings")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, fmt.Errorf("create Cline settings directory: %w", err)
	}
	baseURL := joinBase(apiBase, "/v1")
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	settings := clineProviderSettingsFile{
		Version:          1,
		LastUsedProvider: selectedProvider,
		Providers:        make(map[string]clineProviderProfile, 2),
	}
	catalog := clineModelCatalogFile{
		Version:   1,
		Providers: make(map[string]clineCatalogProvider, 2),
	}
	addProvider := func(id, name, protocol, client string, providerModels []Model) {
		if len(providerModels) == 0 {
			return
		}
		defaultModel := providerModels[0].ID
		if id == selectedProvider {
			defaultModel = selected
		}
		settings.Providers[id] = clineProviderProfile{
			Settings: clineProviderSettings{
				Provider: id,
				APIKey:   token,
				Model:    defaultModel,
				BaseURL:  baseURL,
			},
			UpdatedAt:   updatedAt,
			TokenSource: "manual",
		}
		catalogModels := make(map[string]clineCatalogModel, len(providerModels))
		for _, model := range providerModels {
			displayName := strings.TrimSpace(model.DisplayName)
			if displayName == "" {
				displayName = model.ID
			}
			catalogModels[model.ID] = clineCatalogModel{ID: model.ID, Name: displayName}
		}
		catalog.Providers[id] = clineCatalogProvider{
			Provider: clineCatalogProviderInfo{
				Name:           name,
				BaseURL:        baseURL,
				DefaultModelID: defaultModel,
				Protocol:       protocol,
				Client:         client,
				Capabilities:   []string{"tools"},
			},
			Models: catalogModels,
		}
	}
	addProvider(clineChatProviderID, "EveryAPI Chat", "openai-chat", "openai-compatible", chatModels)
	addProvider(clineResponsesProviderID, "EveryAPI Responses", "openai-responses", "openai", responsesModels)

	catalogBody, err := json.Marshal(catalog)
	if err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, fmt.Errorf("encode Cline model catalog: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(settingsDir, "models.json"), catalogBody, 0o600); err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, err
	}
	body, err := json.Marshal(settings)
	if err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, fmt.Errorf("encode Cline provider settings: %w", err)
	}
	path := filepath.Join(settingsDir, "providers.json")
	if err := writeFileAtomic(path, body, 0o600); err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, err
	}
	env := preparedHomeEnv("CLINE_DATA_DIR", home)
	env["CLINE_PROVIDER_SETTINGS_PATH"] = path
	return env, nil
}
