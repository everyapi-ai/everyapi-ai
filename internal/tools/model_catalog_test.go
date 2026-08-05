package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testLaunchCatalog = []Model{
	{ID: "gpt-5.6-terra", OwnedBy: "openai", SupportedEndpointTypes: []string{"openai", "openai-response"}},
	{ID: "claude-sonnet-test", OwnedBy: "anthropic", SupportedEndpointTypes: []string{"anthropic"}},
}

func TestClaudeEnablesGatewayModelDiscovery(t *testing.T) {
	tool, err := Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	env := tool.Env("https://api.everyapi.ai", "relay-key")
	if got := env["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"]; got != "1" {
		t.Fatalf("CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY = %q, want 1", got)
	}
	if tool.RequiredEndpoint != "anthropic" {
		t.Fatalf("Claude RequiredEndpoint = %q, want anthropic fail-closed preflight", tool.RequiredEndpoint)
	}
}

func TestQwenPrepareWithModelsWritesNativePickerCatalog(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	systemDir := t.TempDir()
	systemPath := filepath.Join(systemDir, "settings.json")
	t.Setenv("QWEN_CODE_SYSTEM_SETTINGS_PATH", systemPath)
	if err := os.WriteFile(systemPath, []byte(`{"security":{"disableYoloMode":true},"modelProviders":{"anthropic":[{"id":"managed"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tool, _ := Lookup("qwen-code")

	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1])
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	home := extra["QWEN_HOME"]
	if home == "" {
		t.Fatal("QWEN_HOME was not prepared")
	}
	if _, ok := extra["QWEN_CODE_SYSTEM_SETTINGS_PATH"]; ok {
		t.Fatal("Qwen preparation must not impersonate or override administrator system settings")
	}
	body, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-relay-key") {
		t.Fatal("Qwen settings persisted the relay credential")
	}
	var settings map[string]any
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	providers := settings["modelProviders"].(map[string]any)
	models := providers["openai"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["id"] != "gpt-5.6-terra" {
		t.Fatalf("unexpected Qwen modelProviders: %#v", providers)
	}
	systemBody, err := os.ReadFile(systemPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(systemBody) != `{"security":{"disableYoloMode":true},"modelProviders":{"anthropic":[{"id":"managed"}]}}` {
		t.Fatalf("Qwen system policy was modified: %s", systemBody)
	}
}

func TestQwenPrepareRejectsHigherPrecedenceCatalogOverrides(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tool, _ := Lookup("qwen-code")

	t.Run("administrator system", func(t *testing.T) {
		systemPath := filepath.Join(t.TempDir(), "settings.json")
		t.Setenv("QWEN_CODE_SYSTEM_SETTINGS_PATH", systemPath)
		if err := os.WriteFile(systemPath, []byte("{\n // managed catalog\n \"modelProviders\": {\"openai\": []}\n}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.PrepareWithModels("https://api.everyapi.ai", "key", testLaunchCatalog[:1]); err == nil || !strings.Contains(err.Error(), "would override EveryAPI's live catalog") {
			t.Fatalf("system OpenAI catalog conflict was not rejected: %v", err)
		}
	})

	t.Run("workspace", func(t *testing.T) {
		t.Setenv("QWEN_CODE_SYSTEM_SETTINGS_PATH", filepath.Join(t.TempDir(), "missing.json"))
		workspace := t.TempDir()
		t.Chdir(workspace)
		if err := os.MkdirAll(filepath.Join(workspace, ".qwen"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, ".qwen", "settings.json"), []byte(`{"modelProviders":{"openai":[{"id":"workspace-only"}]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.PrepareWithModels("https://api.everyapi.ai", "key", testLaunchCatalog[:1]); err == nil || !strings.Contains(err.Error(), "workspace settings") {
			t.Fatalf("workspace OpenAI catalog conflict was not rejected: %v", err)
		}
	})
}

func TestKimiPrepareWithModelsWritesAliasesWithoutCredential(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("KIMI_MODEL_NAME", "gpt-5.6-terra")
	tool, _ := Lookup("kimi-code")
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1])
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	path := filepath.Join(extra["KIMI_CODE_HOME"], "config.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "secret-relay-key") {
		t.Fatal("Kimi config persisted the relay credential")
	}
	for _, want := range []string{`[models."gpt-5.6-terra"]`, `model = "gpt-5.6-terra"`, `provider = "__kimi_env__"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("Kimi config missing %q:\n%s", want, text)
		}
	}
}

func TestCatalogPreparationsUseIndependentHomesAndCleanThem(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tool, _ := Lookup("qwen-code")
	first, err := tool.PrepareWithModels("https://api.everyapi.ai", "key-a", testLaunchCatalog[:1])
	if err != nil {
		t.Fatal(err)
	}
	second, err := tool.PrepareWithModels("https://api.everyapi.ai", "key-b", testLaunchCatalog[:1])
	if err != nil {
		t.Fatal(err)
	}
	firstHome, secondHome := first["QWEN_HOME"], second["QWEN_HOME"]
	if firstHome == secondHome {
		t.Fatalf("concurrent launches share QWEN_HOME %q", firstHome)
	}
	cleanupFirst := TakePreparedCleanup(first)
	cleanupSecond := TakePreparedCleanup(second)
	cleanupFirst()
	cleanupFirst()
	cleanupSecond()
	for _, home := range []string{firstHome, secondHome} {
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("prepared home survived cleanup: %s (%v)", home, err)
		}
	}
}

func TestPreparedHomeCleanupWaitsOutImmediateWorkerRecreation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "session")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := os.Stat(home); os.IsNotExist(err) {
				_ = os.MkdirAll(filepath.Join(home, "debug"), 0o700)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	removePreparedHomeAfterQuiet(home)
	<-done
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("worker-recreated prepared home survived cleanup: %v", err)
	}
}
