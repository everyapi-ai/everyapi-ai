package tools

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const copilotModelEnv = "COPILOT_MODEL"

const (
	copilotDefaultMaxPromptTokens = 128000
	copilotDefaultMaxOutputTokens = 8192
)

// prepareCopilotWithModels selects the wire API for GitHub Copilot CLI's documented BYOK environment contract. All provider state stays process scoped; the relay credential itself is supplied by Tool.Env and never written to Copilot's persistent settings.
func prepareCopilotWithModels(_ string, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(copilotModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", copilotModelEnv)
	}
	for _, model := range models {
		if strings.TrimSpace(model.ID) != selected {
			continue
		}
		wireAPI := "completions"
		if modelSupportsEndpoint(model.SupportedEndpointTypes, "openai-response") {
			wireAPI = "responses"
		}
		maxOutput := model.MaxOutput
		if maxOutput <= 0 {
			maxOutput = copilotDefaultMaxOutputTokens
		}
		maxPrompt := copilotDefaultMaxPromptTokens
		if model.ContextWindow > 0 {
			maxPrompt = model.ContextWindow
		}
		return map[string]string{
			"COPILOT_PROVIDER_WIRE_API":          wireAPI,
			"COPILOT_PROVIDER_MAX_PROMPT_TOKENS": strconv.Itoa(maxPrompt),
			"COPILOT_PROVIDER_MAX_OUTPUT_TOKENS": strconv.Itoa(maxOutput),
		}, nil
	}
	return nil, fmt.Errorf("%s=%q is absent from the launch catalog", copilotModelEnv, selected)
}
