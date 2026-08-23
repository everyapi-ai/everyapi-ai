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

	cfgDir, err := config.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve everyapi config dir: %w", err)
	}
	dir := filepath.Join(cfgDir, "gemini")
	if err := applyGeminiAgentContext(settings, dir); err != nil {
		return nil, err
	}

	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal Gemini system settings overlay: %w", err)
	}
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

// applyGeminiAgentContext points Gemini CLI at an EveryAPI-owned context directory through `context.includeDirectories`, its documented mechanism for sourcing context files from outside the workspace.
//
// This is the one adapter where the injection is NOT written into a temporary prepared home, and the reason is the same one that shapes the rest of this file: Gemini CLI's context hierarchy reads ~/.gemini/GEMINI.md and the workspace's own files, and prepareGemini exists precisely to leave both alone. The settings overlay at GEMINI_CLI_SYSTEM_SETTINGS_PATH is a file EveryAPI already creates and owns, so adding a directory it also owns keeps every user-owned path untouched.
//
// The directory is appended, never assigned: a system-scope settings file wins over the user's, so replacing includeDirectories would silently drop directories the user configured. loadFromIncludeDirectories has to be enabled for the entry to be read at all, and that is a real behaviour change worth knowing about — a user who had configured include directories with loading off will now see them loaded. It is documented rather than hidden.
func applyGeminiAgentContext(settings map[string]any, everyapiDir string) error {
	if !agentContextFileEnabled() {
		return nil
	}
	instructions := AgentInstructions()
	if instructions == "" {
		return nil
	}
	contextDir := filepath.Join(everyapiDir, "context")
	if err := os.MkdirAll(contextDir, 0o700); err != nil {
		return fmt.Errorf("create Gemini context directory: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(contextDir, "GEMINI.md"), []byte(instructions+"\n"), 0o600); err != nil {
		return err
	}
	contextSettings, err := objectField(settings, "context")
	if err != nil {
		return err
	}
	existing, err := stringSliceField(contextSettings, "includeDirectories")
	if err != nil {
		return err
	}
	for _, dir := range existing {
		if dir == contextDir {
			contextSettings["loadFromIncludeDirectories"] = true
			return nil
		}
	}
	contextSettings["includeDirectories"] = append(existing, contextDir)
	contextSettings["loadFromIncludeDirectories"] = true
	return nil
}

// stringSliceField reads an existing string array out of decoded JSON, where every element arrives as `any`. A field holding something else is an error rather than a silent reset — the file may be the user's own system-scope settings.
func stringSliceField(parent map[string]any, key string) ([]string, error) {
	value, ok := parent[key]
	if !ok || value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("Gemini system setting %q must be an array", key)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("Gemini system setting %q must hold strings", key)
		}
		out = append(out, text)
	}
	return out, nil
}
