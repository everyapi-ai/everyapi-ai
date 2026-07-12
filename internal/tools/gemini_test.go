package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrepareGemini_PreservesSystemSettingsAndPinsAPIKeyAuth(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	systemPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("GEMINI_CLI_SYSTEM_SETTINGS_PATH", systemPath)
	if err := os.WriteFile(systemPath, []byte(`{"security":{"disableYoloMode":true,"auth":{"enforcedType":"gemini-api-key"}},"tools":{"sandbox":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	extra, err := prepareGemini("https://api.everyapi.ai", "token")
	if err != nil {
		t.Fatalf("prepareGemini: %v", err)
	}
	overlay := extra["GEMINI_CLI_SYSTEM_SETTINGS_PATH"]
	if overlay == "" || overlay == systemPath {
		t.Fatalf("overlay path = %q, must be an EveryAPI-owned copy", overlay)
	}
	body, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("invalid overlay JSON: %v", err)
	}
	security := got["security"].(map[string]any)
	auth := security["auth"].(map[string]any)
	if auth["selectedType"] != "gemini-api-key" {
		t.Errorf("selectedType = %v", auth["selectedType"])
	}
	if auth["enforcedType"] != "gemini-api-key" || security["disableYoloMode"] != true {
		t.Errorf("security policy was not preserved: %#v", security)
	}
	if got["tools"].(map[string]any)["sandbox"] != true {
		t.Errorf("unrelated settings were not preserved: %#v", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(overlay)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("overlay permissions = %o, want 0600", info.Mode().Perm())
		}
	}
}

func TestGeminiTool_PrepareWired(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GEMINI_CLI_SYSTEM_SETTINGS_PATH", filepath.Join(t.TempDir(), "missing.json"))
	tool, _ := Lookup("gemini")
	extra, err := tool.Prepare("https://api.everyapi.ai", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if extra["GEMINI_CLI_SYSTEM_SETTINGS_PATH"] == "" {
		t.Fatal("gemini prepare hook was not invoked")
	}
}
