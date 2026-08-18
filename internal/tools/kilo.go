package tools

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const kiloModelEnv = "EVERYAPI_KILO_MODEL"

var (
	kiloDottedGPTVersion = regexp.MustCompile(`gpt-(\d+)\.(\d+)`)
	kiloMajorGPTVersion  = regexp.MustCompile(`gpt-(\d+)`)
)

func kiloInjectsPromptCacheBreakpoint(modelID string) bool {
	if matches := kiloDottedGPTVersion.FindStringSubmatch(modelID); len(matches) == 3 {
		major, majorErr := strconv.Atoi(matches[1])
		minor, minorErr := strconv.Atoi(matches[2])
		return majorErr == nil && minorErr == nil && (major > 5 || major == 5 && minor >= 6)
	}
	if matches := kiloMajorGPTVersion.FindStringSubmatch(modelID); len(matches) == 2 {
		major, err := strconv.Atoi(matches[1])
		return err == nil && major >= 6
	}
	return false
}

func prepareKiloWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	selected := strings.TrimSpace(os.Getenv(kiloModelEnv))
	if selected == "" {
		return nil, fmt.Errorf("%s is required", kiloModelEnv)
	}
	// Kilo 7.4.22 injects prompt_cache_breakpoint into GPT-5.6+/GPT-6+ Responses requests whenever a custom provider uses @ai-sdk/openai. ChatGPT-backed Codex channels reject that field, while EveryAPI's chat endpoint already bridges these models to Responses upstream. Move only the affected cohort to the compatible chat SDK; unaffected Responses models must retain their native protocol.
	compatibleModels := append([]Model(nil), models...)
	for index := range compatibleModels {
		if modelSupportsEndpoint(compatibleModels[index].SupportedEndpointTypes, "openai-response") &&
			kiloInjectsPromptCacheBreakpoint(compatibleModels[index].ID) {
			compatibleModels[index].SupportedEndpointTypes = []string{"openai"}
		}
	}
	prepared, err := prepareOpenCodeWithModels(apiBase, "", compatibleModels, selected)
	if err != nil {
		return nil, err
	}
	config := prepared["OPENCODE_CONFIG_CONTENT"]
	if config == "" {
		if home := prepared[preparedHomeMarker]; home != "" {
			removePreparedHomeAfterQuiet(home)
		}
		return nil, fmt.Errorf("Kilo provider config is empty")
	}
	home := prepared[preparedHomeMarker]
	if home == "" {
		home, err = newPreparedHome("kilo")
		if err != nil {
			return nil, err
		}
	}
	env := preparedHomeEnv("KILO_CONFIG_DIR", home)
	env["KILO_CONFIG_CONTENT"] = config
	env["KILO_DISABLE_PROJECT_CONFIG"] = "true"
	return env, nil
}
