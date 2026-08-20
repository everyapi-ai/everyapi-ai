package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readPiModels(t *testing.T, path string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatalf("models.json is not valid JSON: %v\n%s", err, body)
	}
	return config
}

func TestPiWebRegistersEveryAPIInTheDurableAgentDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(piAgentDirEnv, "")

	tool, err := Lookup("pi-web")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "pi-web" {
		t.Fatalf("ExecName = %q", tool.ExecName)
	}
	if len(tool.DefaultArgs) != 0 {
		t.Fatalf("DefaultArgs = %v, want none: pi-web takes no subcommand", tool.DefaultArgs)
	}
	if got := tool.Env("https://api.everyapi.ai", "sk-everyapi-test")[openClawCredentialEnv]; got != "sk-everyapi-test" {
		t.Fatalf("%s = %q, want the relay key in the process environment", openClawCredentialEnv, got)
	}

	env, err := tool.PrepareWithModels("https://api.everyapi.ai", "sk-everyapi-test", []Model{
		{ID: "chat-model", DisplayName: "Chat Model", SupportedEndpointTypes: []string{"openai"}, ContextWindow: 400_000, MaxOutput: 64_000, SupportsThinking: true},
		{ID: "responses-model", SupportedEndpointTypes: []string{"openai-response"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Fatalf("env = %#v, want no extra process variables", env)
	}

	modelsPath := filepath.Join(home, ".pi", "agent", "models.json")
	config := readPiModels(t, modelsPath)
	provider := config["providers"].(map[string]any)["everyapi"].(map[string]any)
	if provider["baseUrl"] != "https://api.everyapi.ai/v1" {
		t.Errorf("baseUrl = %v", provider["baseUrl"])
	}
	if provider["apiKey"] != "$EVERYAPI_RELAY_KEY" {
		t.Errorf("apiKey = %v, want the environment reference and never the key itself", provider["apiKey"])
	}
	models := provider["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("models = %v", models)
	}
	first := models[0].(map[string]any)
	if first["api"] != "openai-completions" || first["id"] != "chat-model" || first["name"] != "Chat Model" {
		t.Errorf("first model = %v", first)
	}
	if first["reasoning"] != true || first["contextWindow"] != float64(400_000) || first["maxTokens"] != float64(64_000) {
		t.Errorf("first model lost its gateway metadata: %v", first)
	}
	if second := models[1].(map[string]any); second["api"] != "openai-responses" || second["name"] != "responses-model" {
		t.Errorf("second model = %v", second)
	}

	info, err := os.Stat(modelsPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 && perm != 0o666 {
		t.Errorf("models.json mode = %v, want owner-only", perm)
	}
}

func TestPiWebPreservesUnrelatedProvidersAndKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	agentDir := filepath.Join(home, "custom-agent")
	t.Setenv(piAgentDirEnv, agentDir)
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := `{"providers":{"anthropic":{"apiKey":"keep-me"},"everyapi":{"baseUrl":"https://stale.example/v1","models":[]}},"modelOverrides":{"gpt-5":{"maxTokens":1}}}`
	modelsPath := filepath.Join(agentDir, "models.json")
	if err := os.WriteFile(modelsPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := preparePiWebWithModels("https://api.everyapi.ai", "", []Model{
		{ID: "chat-model", SupportedEndpointTypes: []string{"openai"}},
	}); err != nil {
		t.Fatal(err)
	}

	config := readPiModels(t, modelsPath)
	if _, ok := config["modelOverrides"]; !ok {
		t.Errorf("modelOverrides was dropped: %v", config)
	}
	providers := config["providers"].(map[string]any)
	anthropic, ok := providers["anthropic"].(map[string]any)
	if !ok || anthropic["apiKey"] != "keep-me" {
		t.Errorf("unrelated provider was rewritten: %v", providers["anthropic"])
	}
	everyapi := providers["everyapi"].(map[string]any)
	if everyapi["baseUrl"] != "https://api.everyapi.ai/v1" {
		t.Errorf("stale EveryAPI provider was not refreshed: %v", everyapi)
	}
	if models := everyapi["models"].([]any); len(models) != 1 {
		t.Errorf("models = %v", models)
	}
}

func TestPiWebRefusesAnUnsafeOrMalformedModelsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	agentDir := filepath.Join(home, "agent")
	t.Setenv(piAgentDirEnv, agentDir)
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	modelsPath := filepath.Join(agentDir, "models.json")
	if err := os.WriteFile(modelsPath, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := preparePiWebWithModels("https://api.everyapi.ai", "", nil); err == nil {
		t.Fatal("a non-object models.json must not be overwritten")
	}

	if err := os.Remove(modelsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "elsewhere.json"), modelsPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := preparePiWebWithModels("https://api.everyapi.ai", "", nil); err == nil {
		t.Fatal("a symlinked models.json must not be followed")
	}
}
