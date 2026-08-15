package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const continueModelEnv = "EVERYAPI_CONTINUE_MODEL"

func prepareContinueWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(continueModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", continueModelEnv)
	}
	if strings.Contains(selected, "${{") {
		return nil, fmt.Errorf("%s contains a Continue template expression", continueModelEnv)
	}
	home, err := newPreparedHome("continue")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, "config.yaml")
	var body strings.Builder
	fmt.Fprintf(&body, `name: %q
version: 1.0.0
schema: v1
models:
`, "EveryAPI")
	seen := make(map[string]bool, len(models)+1)
	appendModel := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || strings.Contains(id, "${{") || seen[id] {
			return
		}
		seen[id] = true
		fmt.Fprintf(&body, `  - name: %q
    provider: openai
    model: %q
    apiBase: %q
    apiKey: ${{ secrets.EVERYAPI_RELAY_KEY }}
`, "EveryAPI "+id, id, joinBase(apiBase, "/v1"))
	}
	appendModel(selected)
	for _, model := range models {
		appendModel(model.ID)
	}
	if err := writeFileAtomic(path, []byte(body.String()), 0o600); err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, err
	}
	args, err := json.Marshal([]string{"--config", path})
	if err != nil {
		removePreparedHomeAfterQuiet(home)
		return nil, fmt.Errorf("encode Continue runtime arguments: %w", err)
	}
	env := preparedHomeEnv("CONTINUE_GLOBAL_DIR", home)
	env[preparedArgvMarker] = string(args)
	return env, nil
}
