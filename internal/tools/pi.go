package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	piModelEnv    = "EVERYAPI_PI_MODEL"
	piAgentDirEnv = "PI_CODING_AGENT_DIR"
	// piCredentialRef is the environment reference written into models.json instead of the relay key itself. Pi resolves a "$NAME" config value from the process environment, so the generated provider carries no credential on disk and reads as unconfigured to a pi launched outside EveryAPI.
	piCredentialRef = "$" + openClawCredentialEnv
)

// piUserResources are the Pi agent-directory entries that hold user-authored content rather than provider configuration. The isolated home only owns `models.json` and the selected-model settings, so these have to be pointed back at the user's own directory or a launch silently loses them.
var piUserResources = []string{"extensions", "skills", "prompts", "themes"}

func preparePiWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(piModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", piModelEnv)
	}
	home, err := newPreparedHome("pi")
	if err != nil {
		return nil, err
	}
	config := map[string]any{"providers": map[string]any{"everyapi": piProviderNode(apiBase, models)}}
	body, err := json.Marshal(config)
	if err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, fmt.Errorf("encode Pi model catalog: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(home, "models.json"), body, 0o600); err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, err
	}
	settingsConfig := map[string]any{
		"defaultProvider": "everyapi",
		"defaultModel":    selected,
	}
	// The level the launcher resolved. Pi keeps its own thinking level in this settings.json, which lives in the process-scoped agent dir `everyapi use` deletes on exit, so seeding it is the only way a choice survives to the next launch.
	if level := selectedReasoningLevel(); level != "" {
		settingsConfig["defaultThinkingLevel"] = level
	}
	if piUserDir := piUserAgentDir(); piUserDir != "" {
		for _, resource := range piUserResources {
			resourceDir := filepath.Join(piUserDir, resource)
			if info, statErr := os.Stat(resourceDir); statErr == nil && info.IsDir() {
				settingsConfig[resource] = []string{resourceDir}
			}
		}
	}
	settings, err := json.Marshal(settingsConfig)
	if err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, fmt.Errorf("encode Pi settings: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(home, "settings.json"), settings, 0o600); err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, err
	}
	return preparedHomeEnv(piAgentDirEnv, home), nil
}

// piUserAgentDir resolves the agent directory Pi would have used without EveryAPI's isolation. An operator-set PI_CODING_AGENT_DIR wins over the default ~/.pi/agent, matching how Pi itself locates the directory; the result is made absolute because the generated settings are read from an unrelated working directory. It returns "" when neither source resolves.
func piUserAgentDir() string {
	dir := strings.TrimSpace(os.Getenv(piAgentDirEnv))
	if dir == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(userHome, ".pi", "agent")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return abs
}

// piProviderNode builds the `providers.everyapi` entry both Pi surfaces share. Pi CLI writes it into a process-scoped agent directory; Pi Web merges it into the durable one. Neither carries the relay key: apiKey is an environment reference the launching process supplies.
func piProviderNode(apiBase string, models []Model) map[string]any {
	providerModels := make([]map[string]any, 0, len(models))
	for _, model := range models {
		name := model.DisplayName
		if name == "" {
			name = model.ID
		}
		api := "openai-completions"
		if !modelSupportsEndpoint(model.SupportedEndpointTypes, "openai") &&
			modelSupportsEndpoint(model.SupportedEndpointTypes, "openai-response") {
			api = "openai-responses"
		}
		entry := map[string]any{"api": api, "id": model.ID, "name": name}
		// Most verified models use Pi's standard off→high provider defaults. Models with a narrower or extended upstream contract provide a model-specific map from the same source used by EveryAPI's launch picker, keeping both controls from offering an effort the API rejects.
		if model.SupportsThinking {
			entry["reasoning"] = true
			if levelMap := piThinkingSupportForModel(model).levelMap; levelMap != nil {
				entry["thinkingLevelMap"] = levelMap
			}
		}
		// Pi's own fallbacks are 128000/16384 for a custom provider, so a model with a larger window silently lost most of it — pi compacts against the number it holds, not the one the gateway serves.
		if model.ContextWindow > 0 {
			entry["contextWindow"] = model.ContextWindow
		}
		if model.MaxOutput > 0 {
			entry["maxTokens"] = model.MaxOutput
		}
		providerModels = append(providerModels, entry)
	}
	return map[string]any{
		"baseUrl": joinBase(apiBase, "/v1"),
		"apiKey":  piCredentialRef,
		"models":  providerModels,
	}
}

// preparePiWebWithModels registers EveryAPI in the agent directory Pi Web actually reads, rather than the process-scoped one `everyapi use pi` builds.
//
// Pi Web is a durable local web app over that directory: sessions, project trust, drafts, the selected model, and every edit its Models panel makes all live there and are the whole reason to open it. Handing it a temporary directory would show an empty session list on every launch and discard the user's own configuration on exit, so this merges one provider into the real models.json and leaves the rest of the file untouched.
//
// Nothing secret is written. The provider's apiKey is the fixed "$EVERYAPI_RELAY_KEY" reference, which resolves from the launched process's environment; a pi or Pi Web started outside EveryAPI simply reports the provider as unconfigured. That is also why logout has nothing to scrub here.
func preparePiWebWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	agentDir := piUserAgentDir()
	if agentDir == "" {
		return nil, fmt.Errorf("resolve Pi agent directory")
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return nil, fmt.Errorf("create Pi agent directory: %w", err)
	}
	modelsPath := filepath.Join(agentDir, "models.json")
	config, err := loadPiModelsConfig(modelsPath)
	if err != nil {
		return nil, err
	}
	providers, ok := config["providers"].(map[string]any)
	if !ok {
		if _, present := config["providers"]; present && config["providers"] != nil {
			return nil, fmt.Errorf("Pi models config %s: providers must be a JSON object", modelsPath)
		}
		providers = map[string]any{}
		config["providers"] = providers
	}
	providers["everyapi"] = piProviderNode(apiBase, models)
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Pi models config: %w", err)
	}
	if err := writeFileAtomic(modelsPath, body, 0o600); err != nil {
		return nil, err
	}
	return map[string]string{}, nil
}

// loadPiModelsConfig reads the durable models.json as a generic object so unrelated providers, model overrides, and future keys survive the merge. A missing file starts empty; anything that is not a regular file or not a JSON object is an error rather than something to overwrite.
func loadPiModelsConfig(path string) (map[string]any, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("refuse unsafe Pi models config path %s", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect Pi models config %s: %w", path, err)
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Pi models config %s: %w", path, err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}
	var config map[string]any
	if err := json.Unmarshal(body, &config); err != nil {
		return nil, fmt.Errorf("parse Pi models config %s: %w", path, err)
	}
	if config == nil {
		return nil, fmt.Errorf("Pi models config %s must be a JSON object", path)
	}
	return config, nil
}
