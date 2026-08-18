package tools

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
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
	if got := env["CLAUDE_CODE_USE_GATEWAY"]; got != "1" {
		t.Fatalf("CLAUDE_CODE_USE_GATEWAY = %q, want 1 so Claude actually fetches /v1/models", got)
	}
	if tool.RequiredEndpoint != "anthropic" {
		t.Fatalf("Claude RequiredEndpoint = %q, want anthropic fail-closed preflight", tool.RequiredEndpoint)
	}
}

func TestTransparentClaudeEnablesGatewayModelDiscoveryAtOfficialOrigin(t *testing.T) {
	env, unset := transparentStandaloneClaudeEnv("/tmp/everyapi-ca.pem")
	if got := env["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"]; got != "1" {
		t.Fatalf("CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY = %q, want 1", got)
	}
	if got := env["CLAUDE_CODE_USE_GATEWAY"]; got != "1" {
		t.Fatalf("CLAUDE_CODE_USE_GATEWAY = %q, want 1 so Claude actually fetches /v1/models", got)
	}
	if got := env["ANTHROPIC_BASE_URL"]; got != "https://api.anthropic.com" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want the official origin intercepted by Connector", got)
	}
	if slices.Contains(unset, "ANTHROPIC_BASE_URL") {
		t.Fatal("transparent Claude unsets ANTHROPIC_BASE_URL, disabling gateway model discovery")
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

	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1], "")
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

func TestCrushPrepareUsesIsolatedCatalogAndEnvironmentKeyReference(t *testing.T) {
	t.Setenv(crushModelEnv, "gpt-5.6-terra")
	tool, _ := Lookup("crush")
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1], "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	body, err := os.ReadFile(filepath.Join(extra["CRUSH_GLOBAL_CONFIG"], "crush.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-relay-key") {
		t.Fatal("Crush config persisted the relay credential")
	}
	if !strings.Contains(string(body), `"api_key":"$EVERYAPI_RELAY_KEY"`) ||
		!strings.Contains(string(body), `"model":"gpt-5.6-terra"`) {
		t.Fatalf("unexpected Crush config: %s", body)
	}
	var config struct {
		Providers map[string]struct {
			Models []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	models := config.Providers["everyapi"].Models
	if len(models) != 1 || models[0].ID != "gpt-5.6-terra" || models[0].Name != "gpt-5.6-terra" {
		t.Fatalf("Crush model picker catalog = %#v, want a non-empty model name", models)
	}
}

func TestClinePrepareUsesLifecycleBoundProviderSettings(t *testing.T) {
	t.Setenv(clineModelEnv, "gpt-5.6-terra")
	tool, _ := Lookup("cline")
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1], "")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := TakePreparedCleanup(extra)
	path := extra["CLINE_PROVIDER_SETTINGS_PATH"]
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		LastUsedProvider string `json:"lastUsedProvider"`
		Providers        map[string]struct {
			Settings map[string]any `json:"settings"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.LastUsedProvider != "lmstudio" {
		t.Fatalf("Cline lastUsedProvider = %q, want Chat provider for a dual-protocol model", settings.LastUsedProvider)
	}
	provider, ok := settings.Providers["lmstudio"]
	if !ok {
		t.Fatalf("Cline providers = %#v, want lmstudio", settings.Providers)
	}
	if provider.Settings["provider"] != "lmstudio" ||
		provider.Settings["apiKey"] != "secret-relay-key" ||
		provider.Settings["model"] != "gpt-5.6-terra" ||
		provider.Settings["baseUrl"] != "https://api.everyapi.ai/v1" {
		t.Fatalf("unexpected Cline provider settings: %#v", provider.Settings)
	}
	if _, ok := provider.Settings["protocol"]; ok {
		t.Fatalf("Cline settings include unsupported protocol field: %#v", provider.Settings)
	}
	if _, ok := provider.Settings["client"]; ok {
		t.Fatalf("Cline settings include unsupported client field: %#v", provider.Settings)
	}
	modelCatalogPath := filepath.Join(extra["CLINE_DATA_DIR"], "settings", "models.json")
	if _, err := os.Stat(modelCatalogPath); err != nil {
		t.Fatalf("Cline lifecycle model catalog missing: %v", err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Cline lifecycle settings survived cleanup: %v", err)
	}
}

func TestClinePreparePublishesPickerCatalogAndDisablesHubReuse(t *testing.T) {
	t.Setenv(clineModelEnv, "gpt-5.6-luna")
	tool, _ := Lookup("cline")
	models := []Model{
		{ID: "MiniMax-M3", SupportedEndpointTypes: []string{"openai"}},
		{ID: "vendor/chat model #1", SupportedEndpointTypes: []string{"openai"}},
		{ID: "gpt-5.6-luna", SupportedEndpointTypes: []string{"openai-response"}},
		{ID: "future-response-model", SupportedEndpointTypes: []string{"openai-response"}},
	}
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", models, "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	if got := extra["CLINE_SESSION_BACKEND_MODE"]; got != "local" {
		t.Fatalf("CLINE_SESSION_BACKEND_MODE = %q, want isolated local runtime", got)
	}

	body, err := os.ReadFile(filepath.Join(extra["CLINE_DATA_DIR"], "settings", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Providers map[string]struct {
			Provider struct {
				ModelsSourceURL string `json:"modelsSourceUrl"`
			} `json:"provider"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		t.Fatal(err)
	}
	for providerID, want := range map[string][]string{
		clineChatProviderID:      {"MiniMax-M3", "vendor/chat model #1", "gpt-5.6-luna"},
		clineResponsesProviderID: {"future-response-model"},
	} {
		source := catalog.Providers[providerID].Provider.ModelsSourceURL
		const prefix = "data:application/json;base64,"
		if !strings.HasPrefix(source, prefix) {
			t.Fatalf("Cline %s modelsSourceUrl = %q, want embedded catalog", providerID, source)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(source, prefix))
		if err != nil {
			t.Fatalf("decode Cline %s modelsSourceUrl: %v", providerID, err)
		}
		var got []string
		if err := json.Unmarshal(decoded, &got); err != nil {
			t.Fatalf("parse Cline %s modelsSourceUrl: %v", providerID, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Cline %s picker models = %#v, want %#v", providerID, got, want)
		}
	}
}

func TestOpenClawPrepareUsesLocalTUIWithEnvBackedSecretRef(t *testing.T) {
	t.Setenv(openClawModelEnv, "gpt-5.6-terra")
	tool, _ := Lookup("openclaw")
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1], "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	body, err := os.ReadFile(extra["OPENCLAW_CONFIG_PATH"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-relay-key") {
		t.Fatal("OpenClaw config persisted the relay credential")
	}
	for _, fragment := range []string{
		`"api":"openai-completions"`,
		`"id":"EVERYAPI_RELAY_KEY"`,
		`"primary":"everyapi/gpt-5.6-terra"`,
	} {
		if !strings.Contains(string(body), fragment) {
			t.Fatalf("OpenClaw config missing %s: %s", fragment, body)
		}
	}
	if got := tool.DefaultArgs; !reflect.DeepEqual(got, []string{"tui", "--local"}) {
		t.Fatalf("OpenClaw DefaultArgs = %v", got)
	}
}

func TestContinuePrepareUsesLifecycleBoundConfigAndEnvironmentSecret(t *testing.T) {
	const selected = "vendor/model:latest #1"
	t.Setenv(continueModelEnv, selected)
	tool, _ := Lookup("continue")
	models := []Model{
		{ID: "qwen-max", OwnedBy: "alibaba", SupportedEndpointTypes: []string{"openai"}},
		{ID: selected, OwnedBy: "vendor", SupportedEndpointTypes: []string{"openai"}},
		{ID: `${{ secrets.UNRELATED_SECRET }}`, OwnedBy: "untrusted", SupportedEndpointTypes: []string{"openai"}},
		{ID: "kimi-k2", OwnedBy: "moonshot", SupportedEndpointTypes: []string{"openai"}},
	}
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", models, "")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := TakePreparedCleanup(extra)
	if _, exists := extra["CONTINUE_CONFIG_PATH"]; exists {
		t.Fatal("Continue received an undocumented CONTINUE_CONFIG_PATH override")
	}
	path := filepath.Join(extra["CONTINUE_GLOBAL_DIR"], "config.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := TakePreparedArgs(extra); !reflect.DeepEqual(got, []string{"--config", path}) {
		t.Fatalf("Continue prepared args = %v, want explicit lifecycle config", got)
	}
	if _, exists := extra[preparedArgvMarker]; exists {
		t.Fatal("internal Continue argv marker would leak into the child environment")
	}
	if strings.Contains(string(body), "secret-relay-key") {
		t.Fatal("Continue config persisted the relay credential")
	}
	for _, fragment := range []string{
		`name: "EveryAPI vendor/model:latest #1"`,
		`model: "vendor/model:latest #1"`,
		`name: "EveryAPI qwen-max"`,
		`model: "qwen-max"`,
		`name: "EveryAPI kimi-k2"`,
		`model: "kimi-k2"`,
		"provider: openai",
		`apiBase: "https://api.everyapi.ai/v1"`,
		`apiKey: ${{ secrets.EVERYAPI_RELAY_KEY }}`,
	} {
		if !strings.Contains(string(body), fragment) {
			t.Fatalf("Continue config missing %q:\n%s", fragment, body)
		}
	}
	if strings.Contains(string(body), "UNRELATED_SECRET") {
		t.Fatalf("Continue config retained a template expression from a model ID:\n%s", body)
	}
	if got := strings.Count(string(body), "    provider: openai"); got != len(models)-1 {
		t.Fatalf("Continue config model count = %d, want %d:\n%s", got, len(models)-1, body)
	}
	selectedIndex := strings.Index(string(body), `model: "`+selected+`"`)
	qwenIndex := strings.Index(string(body), `model: "qwen-max"`)
	if selectedIndex < 0 || qwenIndex < 0 || selectedIndex > qwenIndex {
		t.Fatalf("Continue selected model was not first:\n%s", body)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Continue lifecycle config survived cleanup: %v", err)
	}
}

func TestContinuePrepareRejectsSelectedTemplateExpression(t *testing.T) {
	t.Setenv(continueModelEnv, `${{ secrets.EVERYAPI_RELAY_KEY }}`)
	tool, _ := Lookup("continue")
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", []Model{
		{ID: `${{ secrets.EVERYAPI_RELAY_KEY }}`, SupportedEndpointTypes: []string{"openai"}},
	}, "")
	if extra != nil {
		TakePreparedCleanup(extra)()
	}
	if err == nil || !strings.Contains(err.Error(), "template expression") {
		t.Fatalf("Continue selected template model error = %v, want fail-closed rejection", err)
	}
}

func TestKiloPrepareRoutesResponsesModelsThroughCompatibleChatBridge(t *testing.T) {
	t.Setenv(kiloModelEnv, "gpt-5.6-terra")
	tool, _ := Lookup("kilo")
	if tool.RequiredEndpoint != "openai" || tool.AlternativeEndpoint != "openai-response" {
		t.Fatalf("Kilo endpoint contract = %q/%q", tool.RequiredEndpoint, tool.AlternativeEndpoint)
	}
	models := []Model{
		{ID: "chat-only", DisplayName: "Chat Only", SupportedEndpointTypes: []string{"openai"}},
		{ID: "gpt-5.6-terra", DisplayName: "GPT 5.6 Terra", SupportedEndpointTypes: []string{"openai-response"}},
		{ID: "gpt-5.5-response", DisplayName: "GPT 5.5 Response", SupportedEndpointTypes: []string{"openai-response"}},
	}
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", models, "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	body := extra["KILO_CONFIG_CONTENT"]
	if strings.Contains(body, "secret-relay-key") {
		t.Fatal("Kilo config persisted the relay credential")
	}
	var config openCodeConfig
	if err := json.Unmarshal([]byte(body), &config); err != nil {
		t.Fatalf("decode Kilo config: %v", err)
	}
	chatProvider := config.Provider["everyapi"]
	if chatProvider.NPM != "@ai-sdk/openai-compatible" {
		t.Fatalf("Kilo chat provider npm = %q", chatProvider.NPM)
	}
	if got := chatProvider.Models["chat-only"].Name; got != "Chat Only" {
		t.Fatalf("Kilo chat model name = %q", got)
	}
	if got := chatProvider.Models["gpt-5.6-terra"].Name; got != "GPT 5.6 Terra" {
		t.Fatalf("Kilo bridged Responses model name = %q", got)
	}
	responsesProvider := config.Provider["everyapi-responses"]
	if responsesProvider.NPM != "@ai-sdk/openai" {
		t.Fatalf("Kilo unaffected Responses provider npm = %q", responsesProvider.NPM)
	}
	if got := responsesProvider.Models["gpt-5.5-response"].Name; got != "GPT 5.5 Response" {
		t.Fatalf("Kilo unaffected Responses model name = %q", got)
	}
	if _, ok := responsesProvider.Models["gpt-5.6-terra"]; ok {
		t.Fatal("Kilo GPT-5.6 model remained on the prompt-cache-breakpoint provider")
	}
	if chatProvider.Options.BaseURL != "https://api.everyapi.ai/v1" {
		t.Fatalf("Kilo chat bridge baseURL = %q", chatProvider.Options.BaseURL)
	}
	if chatProvider.Options.APIKey != "{env:EVERYAPI_RELAY_KEY}" {
		t.Fatalf("Kilo chat bridge API key reference = %q", chatProvider.Options.APIKey)
	}
	if config.Model != "everyapi/gpt-5.6-terra" {
		t.Fatalf("Kilo selected model = %q", config.Model)
	}
	for index := 1; index < len(models); index++ {
		if !reflect.DeepEqual(models[index].SupportedEndpointTypes, []string{"openai-response"}) {
			t.Fatalf("Kilo mutated input model %q endpoints = %#v", models[index].ID, models[index].SupportedEndpointTypes)
		}
	}
}

func TestKiloTmuxInstructionsShareItsPreparedHomeLifecycle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv(TerminalModeEnvironment, "tmux")
	t.Setenv(TmuxSessionEnvironment, "everyapi-123-456")
	t.Setenv(TmuxAttachCommandEnvironment, "tmux attach -t everyapi-123-456")
	t.Setenv(kiloModelEnv, "gpt-5")
	extra, err := prepareKiloWithModels("https://api.everyapi.ai", "token", []Model{{ID: "gpt-5", SupportedEndpointTypes: []string{"openai"}}})
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Instructions []string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(extra["KILO_CONFIG_CONTENT"]), &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Instructions) != 1 {
		t.Fatalf("Kilo instructions = %#v, want one process-scoped file", config.Instructions)
	}
	if filepath.Dir(config.Instructions[0]) != extra["KILO_CONFIG_DIR"] {
		t.Fatalf("Kilo instruction path %q is outside prepared home %q", config.Instructions[0], extra["KILO_CONFIG_DIR"])
	}
	cleanup := TakePreparedCleanup(extra)
	if cleanup == nil {
		t.Fatal("Kilo tmux instructions have no lifecycle cleanup")
	}
	cleanup()
	if _, err := os.Stat(config.Instructions[0]); !os.IsNotExist(err) {
		t.Fatalf("Kilo tmux instruction file remained after cleanup: %v", err)
	}
}

func TestKiloPromptCacheBreakpointCohort(t *testing.T) {
	tests := []struct {
		modelID string
		want    bool
	}{
		{modelID: "gpt-5.5-response", want: false},
		{modelID: "gpt-5.6-luna", want: true},
		{modelID: "gpt-5.10", want: true},
		{modelID: "gpt-6", want: true},
		{modelID: "vendor/gpt-6-preview", want: true},
		{modelID: "other-response", want: false},
	}
	for _, test := range tests {
		t.Run(test.modelID, func(t *testing.T) {
			if got := kiloInjectsPromptCacheBreakpoint(test.modelID); got != test.want {
				t.Fatalf("kiloInjectsPromptCacheBreakpoint(%q) = %t, want %t", test.modelID, got, test.want)
			}
		})
	}
}

func TestPiPrepareUsesIsolatedModelsCatalogAndEnvironmentKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir reads USERPROFILE on Windows, and an inherited PI_CODING_AGENT_DIR would take priority over the home default; both have to be pinned or the assertions below resolve against the developer's real machine.
	t.Setenv("USERPROFILE", home)
	t.Setenv(piAgentDirEnv, "")
	for _, resource := range piUserResources {
		if err := os.MkdirAll(filepath.Join(home, ".pi", "agent", resource), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(piModelEnv, "gpt-5.6-terra")
	tool, _ := Lookup("pi")
	if tool.RequiredEndpoint != "openai" || tool.AlternativeEndpoint != "openai-response" {
		t.Fatalf("Pi endpoint contract = %q/%q", tool.RequiredEndpoint, tool.AlternativeEndpoint)
	}
	models := []Model{
		{ID: "gpt-5.6-terra", SupportedEndpointTypes: []string{"openai-response"}},
		{ID: "chat-only", SupportedEndpointTypes: []string{"openai"}},
		{ID: "dual-protocol", SupportedEndpointTypes: []string{"openai", "openai-response"}},
	}
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", models, "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	body, err := os.ReadFile(filepath.Join(extra[piAgentDirEnv], "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-relay-key") {
		t.Fatal("Pi config persisted the relay credential")
	}
	for _, fragment := range []string{
		`"apiKey":"$EVERYAPI_RELAY_KEY"`,
		`"api":"openai-responses","id":"gpt-5.6-terra"`,
		`"api":"openai-completions","id":"chat-only"`,
		`"api":"openai-completions","id":"dual-protocol"`,
	} {
		if !strings.Contains(string(body), fragment) {
			t.Fatalf("Pi config missing %s: %s", fragment, body)
		}
	}
	settings, err := os.ReadFile(filepath.Join(extra[piAgentDirEnv], "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsedSettings struct {
		DefaultProvider string   `json:"defaultProvider"`
		DefaultModel    string   `json:"defaultModel"`
		Extensions      []string `json:"extensions"`
		Skills          []string `json:"skills"`
		Prompts         []string `json:"prompts"`
		Themes          []string `json:"themes"`
	}
	if err := json.Unmarshal(settings, &parsedSettings); err != nil {
		t.Fatal(err)
	}
	if parsedSettings.DefaultProvider != "everyapi" || parsedSettings.DefaultModel != "gpt-5.6-terra" {
		t.Fatalf("Pi selected provider/model = %q/%q", parsedSettings.DefaultProvider, parsedSettings.DefaultModel)
	}
	for resource, got := range map[string][]string{
		"extensions": parsedSettings.Extensions,
		"skills":     parsedSettings.Skills,
		"prompts":    parsedSettings.Prompts,
		"themes":     parsedSettings.Themes,
	} {
		want := []string{filepath.Join(home, ".pi", "agent", resource)}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Pi %s paths = %#v, want %#v", resource, got, want)
		}
	}
}

func TestPiPrepareHonoursExistingAgentDirOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	agentDir := filepath.Join(t.TempDir(), "pi-agent")
	t.Setenv(piAgentDirEnv, agentDir)
	for _, resource := range piUserResources {
		// The home default must stay populated so a passing assertion proves the override won rather than that nothing existed.
		if err := os.MkdirAll(filepath.Join(home, ".pi", "agent", resource), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(agentDir, resource), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(piModelEnv, "gpt-5.6-terra")
	tool, err := Lookup("pi")
	if err != nil {
		t.Fatal(err)
	}
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog, "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	settings, err := os.ReadFile(filepath.Join(extra[piAgentDirEnv], "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsedSettings map[string]any
	if err := json.Unmarshal(settings, &parsedSettings); err != nil {
		t.Fatal(err)
	}
	for _, resource := range piUserResources {
		want := []any{filepath.Join(agentDir, resource)}
		if got := parsedSettings[resource]; !reflect.DeepEqual(got, want) {
			t.Errorf("Pi %s paths = %#v, want %#v", resource, got, want)
		}
	}
}

func TestVibePrepareUsesIsolatedOpenAICompatibleProfile(t *testing.T) {
	t.Setenv(vibeModelEnv, "gpt-5.6-terra")
	tool, _ := Lookup("vibe")
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1], "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	body, err := os.ReadFile(filepath.Join(extra["VIBE_HOME"], "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-relay-key") {
		t.Fatal("Vibe config persisted the relay credential")
	}
	for _, fragment := range []string{`active_model = "gpt-5.6-terra"`, `api_key_env_var = "EVERYAPI_RELAY_KEY"`, `name = "gpt-5.6-terra"`} {
		if !strings.Contains(string(body), fragment) {
			t.Fatalf("Vibe config missing %q:\n%s", fragment, body)
		}
	}
}

func TestCopilotUsesOfficialProcessScopedBYOKContract(t *testing.T) {
	tool, err := Lookup("copilot")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ModelEnv != "COPILOT_MODEL" {
		t.Fatalf("Copilot ModelEnv = %q, want COPILOT_MODEL", tool.ModelEnv)
	}
	if tool.RequiredEndpoint != "openai" || tool.AlternativeEndpoint != "openai-response" {
		t.Fatalf("Copilot endpoint contract = %q/%q", tool.RequiredEndpoint, tool.AlternativeEndpoint)
	}
	env := tool.Env("https://api.everyapi.ai/", "secret-relay-key")
	for key, want := range map[string]string{
		"COPILOT_PROVIDER_BASE_URL": "https://api.everyapi.ai/v1",
		"COPILOT_PROVIDER_TYPE":     "openai",
		"COPILOT_PROVIDER_API_KEY":  "secret-relay-key",
		// Copilot gives both of these higher precedence than API_KEY. Empty process-scoped values prevent ambient provider credentials or header overrides from bypassing EveryAPI.
		"COPILOT_PROVIDER_BEARER_TOKEN": "",
		"COPILOT_PROVIDER_HEADERS":      "",
	} {
		got, exists := env[key]
		if !exists {
			t.Errorf("%s is missing from the process-scoped override", key)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	t.Setenv("COPILOT_MODEL", "gpt-5.6-terra")
	responsesModels := []Model{{
		ID:                     "gpt-5.6-terra",
		SupportedEndpointTypes: []string{"openai-response"},
		ContextWindow:          262144,
		MaxOutput:              32768,
	}}
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", responsesModels, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := extra["COPILOT_PROVIDER_WIRE_API"]; got != "responses" {
		t.Fatalf("Responses-capable model wire API = %q, want responses", got)
	}
	if got := extra["COPILOT_PROVIDER_MAX_PROMPT_TOKENS"]; got != "262144" {
		t.Fatalf("Responses-capable prompt limit = %q, want catalogue context window", got)
	}
	if got := extra["COPILOT_PROVIDER_MAX_OUTPUT_TOKENS"]; got != "32768" {
		t.Fatalf("Responses-capable output limit = %q, want catalogue max output", got)
	}
	if strings.Contains(strings.Join(mapValues(extra), "\n"), "secret-relay-key") {
		t.Fatal("Copilot preparation copied the relay credential outside its documented env contract")
	}

	t.Setenv("COPILOT_MODEL", "chat-only")
	chatOnly := []Model{{ID: "chat-only", SupportedEndpointTypes: []string{"openai"}}}
	extra, err = tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", chatOnly, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := extra["COPILOT_PROVIDER_WIRE_API"]; got != "completions" {
		t.Fatalf("Chat-only model wire API = %q, want completions", got)
	}
	if got := extra["COPILOT_PROVIDER_MAX_PROMPT_TOKENS"]; got != "128000" {
		t.Fatalf("Unknown chat model prompt fallback = %q, want Copilot default", got)
	}
	if got := extra["COPILOT_PROVIDER_MAX_OUTPUT_TOKENS"]; got != "8192" {
		t.Fatalf("Unknown chat model output fallback = %q, want conservative default", got)
	}
}

func TestDroidPrepareUsesOfficialRuntimeSettingsWithoutPersistingCredential(t *testing.T) {
	t.Setenv("EVERYAPI_DROID_MODEL", "gpt-5.6-terra")
	tool, err := Lookup("droid")
	if err != nil {
		t.Fatal(err)
	}
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1], "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	settingsPath := extra[preparedArgsMarker]
	if settingsPath == "" {
		t.Fatal("Droid prepare did not expose its runtime settings path")
	}
	body, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "secret-relay-key") {
		t.Fatal("Droid runtime settings persisted the relay credential")
	}
	for _, fragment := range []string{
		`"model":"custom:EveryAPI-0"`,
		`"model":"gpt-5.6-terra"`,
		`"displayName":"EveryAPI"`,
		`"baseUrl":"https://api.everyapi.ai/v1"`,
		`"apiKey":"${EVERYAPI_RELAY_KEY}"`,
		`"provider":"openai"`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("Droid settings missing %s: %s", fragment, text)
		}
	}
	args := TakePreparedArgs(extra)
	if !reflect.DeepEqual(args, []string{"--settings", settingsPath}) {
		t.Fatalf("Droid prepared args = %v", args)
	}
	if _, exists := extra[preparedArgsMarker]; exists {
		t.Fatal("internal Droid argv marker would leak into the child environment")
	}

	t.Setenv("EVERYAPI_DROID_MODEL", "chat-only")
	chatOnly := []Model{{ID: "chat-only", SupportedEndpointTypes: []string{"openai"}}}
	extra, err = tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", chatOnly, "")
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	body, err = os.ReadFile(extra[preparedArgsMarker])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"provider":"generic-chat-completion-api"`) {
		t.Fatalf("chat-only Droid settings chose the wrong provider: %s", body)
	}
}

func TestOpenHandsUsesOfficialProcessOnlyEnvironmentOverrides(t *testing.T) {
	t.Setenv(openHandsModelEnv, "gpt-5.6-terra")
	tool, err := Lookup("openhands")
	if err != nil {
		t.Fatal(err)
	}
	extra, err := tool.PrepareWithModels(
		"https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1], "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := extra["LLM_MODEL"]; got != "openai/gpt-5.6-terra" {
		t.Fatalf("LLM_MODEL = %q", got)
	}
	env := tool.Env("https://api.everyapi.ai", "secret-relay-key")
	if env["LLM_API_KEY"] != "secret-relay-key" || env["LLM_BASE_URL"] != "https://api.everyapi.ai/v1" {
		t.Fatalf("unexpected OpenHands env: %#v", env)
	}
	if !reflect.DeepEqual(tool.DefaultArgs, []string{"--override-with-envs"}) {
		t.Fatalf("OpenHands DefaultArgs = %v", tool.DefaultArgs)
	}
}

func TestForgePrepareIsolatesCredentialMigrationAndPinsEveryAPIModel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(forgeModelEnv, "gpt-5.6-terra")
	tool, err := Lookup("forge")
	if err != nil {
		t.Fatal(err)
	}
	if tool.RequiredEndpoint != "openai" || tool.AlternativeEndpoint != "openai-response" {
		t.Fatalf("Forge endpoint contract = %q/%q", tool.RequiredEndpoint, tool.AlternativeEndpoint)
	}
	extra, err := tool.PrepareWithModels(
		"https://api.everyapi.ai", "secret-relay-key",
		[]Model{{
			ID:                     "gpt-5.6-terra",
			OwnedBy:                "openai",
			SupportedEndpointTypes: []string{"openai-response"},
		}}, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := TakePreparedCleanup(extra)
	home := extra["FORGE_CONFIG"]
	if home == "" {
		t.Fatal("FORGE_CONFIG was not isolated")
	}
	body, err := os.ReadFile(filepath.Join(home, ".forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-relay-key") {
		t.Fatal("Forge config persisted the relay credential")
	}
	for _, fragment := range []string{`provider_id = "openai_responses_compatible"`, `model_id = "gpt-5.6-terra"`} {
		if !strings.Contains(string(body), fragment) {
			t.Fatalf("Forge config missing %q: %s", fragment, body)
		}
	}
	if extra["FORGE_SESSION__PROVIDER_ID"] != "openai_responses_compatible" || extra["FORGE_SESSION__MODEL_ID"] != "gpt-5.6-terra" {
		t.Fatalf("Forge environment did not pin the session: %#v", extra)
	}
	env := tool.Env("https://api.everyapi.ai", "secret-relay-key")
	if env["OPENAI_URL"] != "https://api.everyapi.ai/v1" || env["OPENAI_API_KEY"] != "secret-relay-key" {
		t.Fatalf("unexpected Forge env: %#v", env)
	}
	cleanup()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("Forge prepared home survived cleanup: %v", err)
	}
}

func TestForgePrepareEncodesArbitraryCatalogModelAsValidTOML(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const modelID = "vendor/model\a"
	t.Setenv(forgeModelEnv, modelID)
	tool, _ := Lookup("forge")
	extra, err := tool.PrepareWithModels(
		"https://api.everyapi.ai", "secret-relay-key",
		[]Model{{ID: modelID, SupportedEndpointTypes: []string{"openai", "openai-response"}}}, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(extra)()
	var decoded struct {
		Session struct {
			ProviderID string `toml:"provider_id"`
			ModelID    string `toml:"model_id"`
		} `toml:"session"`
	}
	if _, err := toml.DecodeFile(filepath.Join(extra["FORGE_CONFIG"], ".forge.toml"), &decoded); err != nil {
		t.Fatalf("decode generated Forge TOML: %v", err)
	}
	if decoded.Session.ProviderID != "openai_compatible" || decoded.Session.ModelID != modelID {
		t.Fatalf("decoded Forge session = %#v", decoded.Session)
	}
}

func TestLLxprtPrepareUsesOfficialEphemeralFlagsAndIsolatedApplicationHomes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(llxprtModelEnv, "gpt-5.6-terra")
	tool, err := Lookup("llxprt")
	if err != nil {
		t.Fatal(err)
	}
	extra, err := tool.PrepareWithModels(
		"https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1], "",
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := TakePreparedCleanup(extra)
	home := extra["LLXPRT_CONFIG_HOME"]
	if home == "" || extra["LLXPRT_DATA_HOME"] != home || extra["LLXPRT_CACHE_HOME"] != home || extra["LLXPRT_LOG_HOME"] != home {
		t.Fatalf("LLxprt homes were not isolated: %#v", extra)
	}
	wantArgs := []string{"--provider", "openai", "--baseurl", "https://api.everyapi.ai/v1", "--model", "gpt-5.6-terra"}
	if got := TakePreparedArgs(extra); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("LLxprt prepared args = %v, want %v", got, wantArgs)
	}
	if env := tool.Env("https://api.everyapi.ai", "secret-relay-key"); env["OPENAI_API_KEY"] != "secret-relay-key" {
		t.Fatalf("unexpected LLxprt env: %#v", env)
	}
	cleanup()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("LLxprt prepared home survived cleanup: %v", err)
	}
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
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
		if _, err := tool.PrepareWithModels("https://api.everyapi.ai", "key", testLaunchCatalog[:1], ""); err == nil || !strings.Contains(err.Error(), "would override EveryAPI's live catalog") {
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
		if _, err := tool.PrepareWithModels("https://api.everyapi.ai", "key", testLaunchCatalog[:1], ""); err == nil || !strings.Contains(err.Error(), "workspace settings") {
			t.Fatalf("workspace OpenAI catalog conflict was not rejected: %v", err)
		}
	})
}

func TestKimiPrepareWithModelsWritesAliasesWithoutCredential(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("KIMI_MODEL_NAME", "gpt-5.6-terra")
	tool, _ := Lookup("kimi-code")
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "secret-relay-key", testLaunchCatalog[:1], "")
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
	first, err := tool.PrepareWithModels("https://api.everyapi.ai", "key-a", testLaunchCatalog[:1], "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := tool.PrepareWithModels("https://api.everyapi.ai", "key-b", testLaunchCatalog[:1], "")
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
