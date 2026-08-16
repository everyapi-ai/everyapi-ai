package tools

import (
	"fmt"
	"os"
	"strings"
)

const openHandsModelEnv = "EVERYAPI_OPENHANDS_MODEL"

// prepareOpenHandsWithModels uses OpenHands CLI's explicit --override-with-envs contract. The selected model remains process scoped and the openai/ prefix tells LiteLLM which compatible wire protocol to use.
func prepareOpenHandsWithModels(_ string, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(openHandsModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", openHandsModelEnv)
	}
	for _, model := range models {
		if strings.TrimSpace(model.ID) == selected {
			return map[string]string{"LLM_MODEL": "openai/" + selected}, nil
		}
	}
	return nil, fmt.Errorf("%s=%q is absent from the launch catalog", openHandsModelEnv, selected)
}
