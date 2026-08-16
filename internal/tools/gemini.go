package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

// prepareGemini pins non-interactive auth to GEMINI_API_KEY through an EveryAPI-owned system-settings overlay. It preserves the user's ~/.gemini directory, including MCP servers, skills, extensions and sessions.
func prepareGemini(_, _ string) (map[string]string, error) {
	settings, err := loadGeminiSystemSettings(geminiSystemSettingsPath())
	if err != nil {
		return nil, err
	}
	security, err := objectField(settings, "security")
	if err != nil {
		return nil, err
	}
	auth, err := objectField(security, "auth")
	if err != nil {
		return nil, err
	}
	auth["selectedType"] = "gemini-api-key"

	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal Gemini system settings overlay: %w", err)
	}
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve everyapi config dir: %w", err)
	}
	dir := filepath.Join(cfgDir, "gemini")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create Gemini config directory: %w", err)
	}
	path := filepath.Join(dir, "system-settings.json")
	if err := writeFileAtomic(path, append(body, '\n'), 0o600); err != nil {
		return nil, err
	}
	return map[string]string{"GEMINI_CLI_SYSTEM_SETTINGS_PATH": path}, nil
}

func prepareGeminiTransparent() (map[string]string, error) {
	return prepareGemini("", "")
}

func geminiSystemSettingsPath() string {
	if path := os.Getenv("GEMINI_CLI_SYSTEM_SETTINGS_PATH"); path != "" {
		return path
	}
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/GeminiCli/settings.json"
	case "windows":
		if root := os.Getenv("ProgramData"); root != "" {
			return filepath.Join(root, "gemini-cli", "settings.json")
		}
		return `C:\ProgramData\gemini-cli\settings.json`
	default:
		return "/etc/gemini-cli/settings.json"
	}
}

func loadGeminiSystemSettings(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return make(map[string]any), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Gemini system settings %s: %w", path, err)
	}
	var settings map[string]any
	if err := json.Unmarshal(body, &settings); err != nil {
		return nil, fmt.Errorf("parse Gemini system settings %s: %w", path, err)
	}
	if settings == nil {
		settings = make(map[string]any)
	}
	return settings, nil
}

func objectField(parent map[string]any, key string) (map[string]any, error) {
	value, ok := parent[key]
	if !ok || value == nil {
		child := make(map[string]any)
		parent[key] = child
		return child, nil
	}
	child, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Gemini system setting %q must be an object", key)
	}
	return child, nil
}
