package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const vibeModelEnv = "EVERYAPI_VIBE_MODEL"

func prepareVibeWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(vibeModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", vibeModelEnv)
	}
	home, err := newPreparedHome("vibe")
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	fmt.Fprintf(&body, "active_model = %q\nenable_telemetry = false\n\n", selected)
	body.WriteString("[[providers]]\nname = \"everyapi\"\n")
	fmt.Fprintf(&body, "api_base = %q\n", joinBase(apiBase, "/v1"))
	body.WriteString("api_key_env_var = \"EVERYAPI_RELAY_KEY\"\nbackend = \"generic\"\n\n")
	for _, model := range models {
		fmt.Fprintf(&body, "[[models]]\nname = %q\nprovider = \"everyapi\"\nalias = %q\n\n", model.ID, model.ID)
	}
	if err := writeFileAtomic(filepath.Join(home, "config.toml"), []byte(body.String()), 0o600); err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, err
	}
	return preparedHomeEnv("VIBE_HOME", home), nil
}
