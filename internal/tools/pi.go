package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	piModelEnv    = "EVERYAPI_PI_MODEL"
	piAgentDirEnv = "PI_CODING_AGENT_DIR"
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
		// Pi defaults an undeclared model to reasoning:false, which is not "unknown" to it — it is a statement, and it disables the thinking-level control (shift+tab) for that model entirely. Every EveryAPI model therefore arrived level-less, including the GPT-5.x line whose whole point is a selectable effort. Declared only where the gateway has verified the model takes one, so a model of unknown shape keeps the safe answer rather than gaining a control that would send reasoning_effort upstream and 400.
		//
		// thinkingLevelMap is deliberately absent: omitting it maps off → high onto pi's default provider values, while xhigh and max stay hidden. Naming those two would need a per-model claim about which extended efforts the upstream accepts (gpt-5.6-sol takes ultra, o4-mini stops at high), and the gateway publishes no such list — supports_thinking is one bit.
		if model.SupportsThinking {
			entry["reasoning"] = true
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
	config := map[string]any{"providers": map[string]any{"everyapi": map[string]any{
		"baseUrl": joinBase(apiBase, "/v1"),
		"apiKey":  "$EVERYAPI_RELAY_KEY", "models": providerModels,
	}}}
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
