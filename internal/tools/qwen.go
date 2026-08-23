package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

// prepareQwenCode isolates Qwen Code from the user's normal ~/.qwen profile without overwriting preferences saved inside the EveryAPI-specific profile. Routing and authentication are pinned through environment variables and the higher-precedence --auth-type argument; the relay credential is never written.
func prepareQwenCode(_, _ string) (map[string]string, error) {
	return prepareQwenCodeWithModels("", "", nil)
}

func prepareQwenCodeWithModels(apiBase, _ string, models []Model) (map[string]string, error) {
	if len(models) > 0 {
		if err := validateQwenModelProviderPrecedence(); err != nil {
			return nil, err
		}
	}
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve everyapi config dir: %w", err)
	}
	qwenHome := filepath.Join(cfgDir, "qwen-home")
	if len(models) > 0 {
		qwenHome, err = newPreparedHome("qwen")
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(qwenHome, 0o700); err != nil {
		return nil, fmt.Errorf("create qwen-home: %w", err)
	}
	// Same reasoning as kimi-code: EveryAPI owns this directory whether or not this launch needed a temporary one.
	addAgentContextToHome(qwenHome)
	extra := map[string]string{"QWEN_HOME": qwenHome}
	if len(models) > 0 {
		if err := writeQwenModelCatalog(qwenHome, apiBase, models); err != nil {
			removePreparedHomeAfterQuiet(qwenHome)
			return nil, err
		}
		extra[preparedHomeMarker] = qwenHome
	}
	return extra, nil
}

func validateQwenModelProviderPrecedence() error {
	paths := []struct {
		path  string
		scope string
	}{{qwenSystemSettingsPath(), "administrator system"}}
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, struct {
			path  string
			scope string
		}{filepath.Join(cwd, ".qwen", "settings.json"), "workspace"})
	}
	for _, candidate := range paths {
		body, err := os.ReadFile(candidate.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read Qwen %s settings %s: %w", candidate.scope, candidate.path, err)
		}
		body, err = stripJSONComments(body)
		if err != nil {
			return fmt.Errorf("parse Qwen %s settings %s: %w", candidate.scope, candidate.path, err)
		}
		var settings map[string]any
		if err := json.Unmarshal(body, &settings); err != nil {
			return fmt.Errorf("parse Qwen %s settings %s: %w", candidate.scope, candidate.path, err)
		}
		if providers, _ := settings["modelProviders"].(map[string]any); providers != nil {
			if _, conflicts := providers["openai"]; conflicts {
				return fmt.Errorf("Qwen %s settings %s define modelProviders.openai and would override EveryAPI's live catalog; remove that entry or launch from a workspace without the override", candidate.scope, candidate.path)
			}
		}
		if candidate.scope == "administrator system" {
			security, _ := settings["security"].(map[string]any)
			auth, _ := security["auth"].(map[string]any)
			if enforced, _ := auth["enforcedType"].(string); enforced != "" && enforced != "openai" {
				return fmt.Errorf("Qwen administrator policy enforces auth type %q; EveryAPI qwen-code requires openai", enforced)
			}
		}
	}
	return nil
}

func qwenSystemSettingsPath() string {
	if path := os.Getenv("QWEN_CODE_SYSTEM_SETTINGS_PATH"); path != "" {
		return path
	}
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/QwenCode/settings.json"
	case "windows":
		return `C:\ProgramData\qwen-code\settings.json`
	default:
		return "/etc/qwen-code/settings.json"
	}
}

func stripJSONComments(body []byte) ([]byte, error) {
	out := append([]byte(nil), body...)
	inString, escaped := false, false
	for i := 0; i < len(out); i++ {
		if inString {
			switch {
			case escaped:
				escaped = false
			case out[i] == '\\':
				escaped = true
			case out[i] == '"':
				inString = false
			}
			continue
		}
		if out[i] == '"' {
			inString = true
			continue
		}
		if out[i] != '/' || i+1 >= len(out) {
			continue
		}
		switch out[i+1] {
		case '/':
			out[i], out[i+1] = ' ', ' '
			i += 2
			for i < len(out) && out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
				i++
			}
			i--
		case '*':
			out[i], out[i+1] = ' ', ' '
			i += 2
			closed := false
			for i < len(out) {
				if i+1 < len(out) && out[i] == '*' && out[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i++
					closed = true
					break
				}
				if out[i] != '\n' && out[i] != '\r' {
					out[i] = ' '
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated block comment")
			}
		}
	}
	return out, nil
}

func writeQwenModelCatalog(qwenHome, apiBase string, models []Model) error {
	path := filepath.Join(qwenHome, "settings.json")
	settings := map[string]any{}
	if body, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(body, &settings); err != nil {
			return fmt.Errorf("parse existing Qwen settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing Qwen settings: %w", err)
	}

	entries := qwenModelEntries(apiBase, models)
	providers, _ := settings["modelProviders"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers["openai"] = entries
	settings["modelProviders"] = providers
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Qwen settings: %w", err)
	}
	return writeFileAtomic(path, append(body, '\n'), 0o600)
}

func qwenModelEntries(apiBase string, models []Model) []map[string]any {
	entries := make([]map[string]any, 0, len(models))
	for _, model := range models {
		entries = append(entries, map[string]any{
			"id": model.ID, "name": model.ID, "envKey": "OPENAI_API_KEY",
			"baseUrl": joinBase(apiBase, "/v1"),
		})
	}
	return entries
}
