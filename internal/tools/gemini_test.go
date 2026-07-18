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

func TestGeminiEntryLaunchesNativeAntigravityCLI(t *testing.T) {
	tool, err := Lookup("gemini")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "agy" {
		t.Fatalf("ExecName = %q, want agy", tool.ExecName)
	}
	if tool.YoloFlag != "--dangerously-skip-permissions" {
		t.Errorf("YoloFlag = %q", tool.YoloFlag)
	}
	if !tool.Native {
		t.Fatal("agy must launch natively without resolving an EveryAPI relay key")
	}
	if tool.SupportsTransparent() {
		t.Fatal("native agy must not receive the EveryAPI transparent connector")
	}
	if env := tool.Env("https://api.everyapi.ai", "secret-relay-key"); len(env) != 0 {
		t.Fatalf("native agy must not receive gateway credentials: %#v", env)
	}
}
