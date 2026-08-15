package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const llxprtModelEnv = "EVERYAPI_LLXPRT_MODEL"

// prepareLLxprtWithModels uses LLxprt's official runtime provider flags and
// redirects all four application roots to one lifecycle-bound directory.
func prepareLLxprtWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(llxprtModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", llxprtModelEnv)
	}
	for _, model := range models {
		if strings.TrimSpace(model.ID) != selected {
			continue
		}
		home, err := newPreparedHome("llxprt")
		if err != nil {
			return nil, err
		}
		args, err := json.Marshal([]string{
			"--provider", "openai",
			"--baseurl", joinBase(apiBase, "/v1"),
			"--model", selected,
		})
		if err != nil {
			removePreparedHomeAfterQuiet(home)
			return nil, fmt.Errorf("encode LLxprt runtime arguments: %w", err)
		}
		return map[string]string{
			preparedHomeMarker:   home,
			preparedArgvMarker:   string(args),
			"LLXPRT_CONFIG_HOME": home,
			"LLXPRT_DATA_HOME":   home,
			"LLXPRT_CACHE_HOME":  home,
			"LLXPRT_LOG_HOME":    home,
		}, nil
	}
	return nil, fmt.Errorf("%s=%q is absent from the launch catalog", llxprtModelEnv, selected)
}
