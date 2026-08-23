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
	env := map[string]string{
		"AIDER_MODEL":               "openai/" + model,
		"GIT_PYTHON_GIT_EXECUTABLE": gitPath,
		"PYTHON_DOTENV_DISABLED":    "1",
	}
	// Aider's documented read-only context surface is `--read <file>`, which adds a file to the chat without making it editable. Passing it on the command line rather than writing `read:` into .aider.conf.yml keeps this to the launch: the user's own conventions file and config stay untouched.
	home, argv, err := agentContextArgv("aider", "--read")
	if err != nil {
		return nil, err
	}
	if home != "" {
		env[preparedHomeMarker] = home
		env[preparedArgvMarker] = argv
	}
	return env, nil
}
