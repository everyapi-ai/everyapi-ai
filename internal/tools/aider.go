package tools

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const aiderModelEnv = "EVERYAPI_AIDER_MODEL"

func prepareAider(_, _ string) (map[string]string, error) {
	model := strings.TrimSpace(os.Getenv(aiderModelEnv))
	if model == "" {
		return nil, fmt.Errorf("%s is required", aiderModelEnv)
	}
	model = strings.TrimPrefix(model, "openai/")
	if model == "" {
		return nil, fmt.Errorf("%s must name a model", aiderModelEnv)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("Aider requires Git, but git is not available: %w", err)
	}
	return map[string]string{
		"AIDER_MODEL":               "openai/" + model,
		"GIT_PYTHON_GIT_EXECUTABLE": gitPath,
		"PYTHON_DOTENV_DISABLED":    "1",
	}, nil
}
