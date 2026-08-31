package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestRegistry_HasExpectedTools pins the V1 supported set. New tools can be added freely; removing one should be a deliberate spec change that breaks this test.
func TestRegistry_HasExpectedTools(t *testing.T) {
	want := []string{
		"claude", "codex", "opencode", "gemini", "antigravity", "aider", "goose", "crush", "cline", "openclaw", "continue", "kilo", "pi", "pi-web", "pi-harness", "vibe", "copilot", "droid", "openhands", "forge", "llxprt", "grok", "qwen-code", "kimi-code", "hermes", "librefang", "open-webui", "deepseek-harness",
	}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("want %d tools, got %d (%v)", len(want), len(got), got)
	}
	for i, n := range want {
		if got[i] != n {
			t.Fatalf("Names()[%d] = %q, want %q", i, got[i], n)
		}
	}
}

func TestClineUsesOfficialCLiteExecutable(t *testing.T) {
	tool, err := Lookup("cline")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "clite" {
		t.Fatalf("Cline ExecName = %q, want official @cline/cli binary clite", tool.ExecName)
	}
}

// TestEnv_Claude verifies the Anthropic env contract: no /v1 suffix (their SDK appends its own version path), token in AUTH_TOKEN not API_KEY (Claude Code's documented variable).
func TestEnv_Claude(t *testing.T) {
	tool, err := Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	env := tool.Env("https://api.everyapi.ai", "my-token")
	if got := env["ANTHROPIC_BASE_URL"]; got != "https://api.everyapi.ai" {
		t.Errorf("ANTHROPIC_BASE_URL = %q", got)
	}
	if got := env["ANTHROPIC_AUTH_TOKEN"]; got != "my-token" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q", got)
	}
	if got := env["ENABLE_TOOL_SEARCH"]; got != "1" {
		t.Errorf("ENABLE_TOOL_SEARCH = %q, want 1", got)
	}
	if got := env["ENABLE_PROMPT_CACHING_1H"]; got != "1" {
		t.Errorf("ENABLE_PROMPT_CACHING_1H = %q, want 1", got)
	}
	if got := env["CLAUDE_CODE_DISABLE_ADVISOR_TOOL"]; got != "1" {
		t.Errorf("CLAUDE_CODE_DISABLE_ADVISOR_TOOL = %q, want 1", got)
	}
	// ANTHROPIC_API_KEY must be present and empty: mergeEnv overlays it onto the child env to neutralise any ambient real Anthropic key so it can't leak to the gateway or shadow ANTHROPIC_AUTH_TOKEN.
	if got, ok := env["ANTHROPIC_API_KEY"]; !ok || got != "" {
		t.Errorf("ANTHROPIC_API_KEY = %q (present=%v), want present and empty", got, ok)
	}
	// No accidental OpenAI vars leaking through.
	if _, ok := env["OPENAI_API_KEY"]; ok {
		t.Error("claude env should not set OPENAI_API_KEY")
	}
}

func TestProviderNamesAreNotTools(t *testing.T) {
	for _, name := range []string{"minimax", "qwen", "deepseek", "byteplus", "glm", "kimi"} {
		if tool, err := Lookup(name); err == nil {
			t.Errorf("Lookup(%q) = %#v, want unknown tool; providers must be selected as models, not launched as CLIs", name, tool)
		}
	}
}

// TestEnv_Codex verifies codex's env contract: only OPENAI_API_KEY. Codex does NOT honor OPENAI_BASE_URL at runtime — its router is pinned to ~/.codex/config.toml's model_provider. The base_url is injected via the prepareFn (CODEX_HOME → config.toml) instead.
func TestEnv_Codex(t *testing.T) {
	tool, _ := Lookup("codex")
	env := tool.Env("https://api.everyapi.ai", "my-token")
	if got := env["OPENAI_API_KEY"]; got != "my-token" {
		t.Errorf("OPENAI_API_KEY = %q", got)
	}
	if _, ok := env["OPENAI_BASE_URL"]; ok {
		// If you're re-adding OPENAI_BASE_URL, double-check codex actually reads it — last time we checked, it doesn't, and setting it created confusing "the env is set but requests still hit api.openai.com" debug sessions.
		t.Error("codex env should not set OPENAI_BASE_URL (codex routes via config.toml model_provider, see prepareFn)")
	}
}

func TestEnv_OpenCodeUsesOfficialCompatibleProviderWithoutPersistingTheKey(t *testing.T) {
	project := t.TempDir()
	projectConfig := filepath.Join(project, "opencode.json")
	originalProjectConfig := []byte(`{"model":"user/provider"}`)
	if err := os.WriteFile(projectConfig, originalProjectConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)

	tool, err := Lookup("opencode")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "opencode" {
		t.Fatalf("ExecName = %q, want opencode", tool.ExecName)
	}
	if tool.RequiredEndpoint != "openai" {
		t.Fatalf("RequiredEndpoint = %q, want openai", tool.RequiredEndpoint)
	}

	const relayKey = "relay-key-must-not-enter-config"
	env := tool.Env("https://api.everyapi.ai", relayKey)
	if got := env["EVERYAPI_RELAY_KEY"]; got != relayKey {
		t.Fatalf("EVERYAPI_RELAY_KEY = %q", got)
	}
	if _, ok := env["OPENAI_API_KEY"]; ok {
		t.Fatal("OpenCode custom provider must not rely on ambient OPENAI_API_KEY")
	}

	extra, err := tool.PrepareWithModels(
		"https://api.everyapi.ai/",
		relayKey,
		[]Model{
			{ID: "gpt-5", DisplayName: "GPT 5", SupportedEndpointTypes: []string{"openai"}},
			{ID: "gpt-5.6-terra", DisplayName: "GPT 5.6 Terra", SupportedEndpointTypes: []string{"openai", "openai-response"}},
			{ID: "claude-sonnet", DisplayName: "Claude Sonnet"},
		},
		"gpt-5.6-terra",
	)
	if err != nil {
		t.Fatal(err)
	}
	content := extra["OPENCODE_CONFIG_CONTENT"]
	if content == "" {
		t.Fatal("PrepareWithModels did not provide OPENCODE_CONFIG_CONTENT")
	}
	if strings.Contains(content, relayKey) {
		t.Fatal("OPENCODE_CONFIG_CONTENT contains the relay key")
	}

	var config struct {
		Model    string `json:"model"`
		Provider map[string]struct {
			NPM     string `json:"npm"`
			Options struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
			Models map[string]struct {
				Name string `json:"name"`
			} `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatalf("parse OPENCODE_CONFIG_CONTENT: %v", err)
	}
	provider := config.Provider["everyapi"]
	if provider.NPM != "@ai-sdk/openai-compatible" {
		t.Errorf("provider npm = %q", provider.NPM)
	}
	if provider.Options.BaseURL != "https://api.everyapi.ai/v1" {
		t.Errorf("baseURL = %q", provider.Options.BaseURL)
	}
	if provider.Options.APIKey != "{env:EVERYAPI_RELAY_KEY}" {
		t.Errorf("apiKey reference = %q", provider.Options.APIKey)
	}
	responsesProvider := config.Provider["everyapi-responses"]
	if responsesProvider.NPM != "@ai-sdk/openai" {
		t.Errorf("responses provider npm = %q", responsesProvider.NPM)
	}
	if responsesProvider.Options.BaseURL != "https://api.everyapi.ai/v1" {
		t.Errorf("responses baseURL = %q", responsesProvider.Options.BaseURL)
	}
	if responsesProvider.Options.APIKey != "{env:EVERYAPI_RELAY_KEY}" {
		t.Errorf("responses apiKey reference = %q", responsesProvider.Options.APIKey)
	}
	if config.Model != "everyapi-responses/gpt-5.6-terra" {
		t.Errorf("model = %q", config.Model)
	}
	if got := provider.Models["gpt-5"].Name; got != "GPT 5" {
		t.Errorf("model display name = %q", got)
	}
	if got := responsesProvider.Models["gpt-5.6-terra"].Name; got != "GPT 5.6 Terra" {
		t.Errorf("responses model display name = %q", got)
	}
	gotProjectConfig, err := os.ReadFile(projectConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotProjectConfig, originalProjectConfig) {
		t.Fatalf("PrepareWithModels modified project opencode.json: %s", gotProjectConfig)
	}
}

func TestPrepareOpenCodeAddsProcessScopedAgentInstructions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv(TerminalModeEnvironment, "tmux")
	t.Setenv(TmuxSessionEnvironment, "everyapi-123-456")
	t.Setenv(TmuxAttachCommandEnvironment, "tmux attach -t everyapi-123-456")
	tool, err := Lookup("opencode")
	if err != nil {
		t.Fatal(err)
	}
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "token", []Model{{ID: "gpt-5", SupportedEndpointTypes: []string{"openai"}}}, "gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Instructions []string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(extra["OPENCODE_CONFIG_CONTENT"]), &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Instructions) != 1 {
		t.Fatalf("OpenCode instructions = %#v, want one process-scoped file", config.Instructions)
	}
	body, err := os.ReadFile(config.Instructions[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != AgentInstructions()+"\n" {
		t.Fatalf("OpenCode agent instructions = %q", body)
	}
	cleanup := TakePreparedCleanup(extra)
	if cleanup == nil {
		t.Fatal("OpenCode agent instruction file has no lifecycle cleanup")
	}
	cleanup()
	if _, err := os.Stat(config.Instructions[0]); !os.IsNotExist(err) {
		t.Fatalf("OpenCode agent instruction file remained after cleanup: %v", err)
	}
}

// TestEnv_Gemini verifies Google's documented Gemini CLI environment contract.
func TestEnv_Gemini(t *testing.T) {
	tool, _ := Lookup("gemini")
	env := tool.Env("https://api.everyapi.ai", "my-token")
	if got := tool.ExecName; got != "gemini" {
		t.Fatalf("ExecName = %q, want gemini", got)
	}
	if got := env["GEMINI_API_KEY"]; got != "my-token" {
		t.Errorf("GEMINI_API_KEY = %q", got)
	}
	if got := env["GOOGLE_GEMINI_BASE_URL"]; got != "https://api.everyapi.ai" {
		t.Errorf("GOOGLE_GEMINI_BASE_URL = %q", got)
	}
	if tool.Native {
		t.Fatal("Gemini CLI must resolve an EveryAPI relay credential")
	}
}

func TestEnv_AntigravityStaysNative(t *testing.T) {
	tool, _ := Lookup("antigravity")
	env := tool.Env("https://api.everyapi.ai", "my-token")
	if got := tool.ExecName; got != "agy" {
		t.Fatalf("ExecName = %q, want agy", got)
	}
	if len(env) != 0 {
		t.Errorf("native agy Env should be empty, got %v", env)
	}
	if !tool.Native {
		t.Fatal("Antigravity must keep its own Google authentication")
	}
}

func TestEnv_AiderUsesOpenAICompatibleEndpoint(t *testing.T) {
	tool, _ := Lookup("aider")
	env := tool.Env("https://api.everyapi.ai/", "my-token")
	for _, key := range []string{"OPENAI_API_KEY", "AIDER_OPENAI_API_KEY"} {
		if got := env[key]; got != "my-token" {
			t.Errorf("%s = %q", key, got)
		}
	}
	for _, key := range []string{"OPENAI_API_BASE", "AIDER_OPENAI_API_BASE"} {
		if got := env[key]; got != "https://api.everyapi.ai/v1" {
			t.Errorf("%s = %q", key, got)
		}
	}
	if tool.ModelEnv != aiderModelEnv || tool.RequiredEndpoint != "openai" {
		t.Fatalf("Aider model/endpoint wiring = %q/%q", tool.ModelEnv, tool.RequiredEndpoint)
	}
}

func TestPrepareAiderPrefixesCatalogModelForLiteLLM(t *testing.T) {
	t.Setenv(aiderModelEnv, "claude-sonnet-4")
	extra, err := prepareAider("https://api.everyapi.ai", "unused")
	if err != nil {
		t.Fatal(err)
	}
	if got := extra["AIDER_MODEL"]; got != "openai/claude-sonnet-4" {
		t.Fatalf("AIDER_MODEL = %q", got)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("test checkout requires git")
	}
	if got := extra["GIT_PYTHON_GIT_EXECUTABLE"]; got != gitPath {
		t.Fatalf("GIT_PYTHON_GIT_EXECUTABLE = %q, want %q", got, gitPath)
	}
	if got := extra["PYTHON_DOTENV_DISABLED"]; got != "1" {
		t.Fatalf("PYTHON_DOTENV_DISABLED = %q, want 1", got)
	}
}

func TestPrepareAiderFailsBeforeLaunchWhenGitIsMissing(t *testing.T) {
	t.Setenv(aiderModelEnv, "claude-sonnet-4")
	t.Setenv("PATH", t.TempDir())
	if _, err := prepareAider("https://api.everyapi.ai", "unused"); err == nil {
		t.Fatal("prepareAider succeeded without git")
	}
}

func TestEnv_GoosePinsOpenAIProviderAndEndpoint(t *testing.T) {
	tool, _ := Lookup("goose")
	env := tool.Env("https://api.everyapi.ai/", "my-token")
	want := map[string]string{
		"GOOSE_PROVIDER":  "openai",
		"OPENAI_API_KEY":  "my-token",
		"OPENAI_BASE_URL": "https://api.everyapi.ai/v1",
	}
	for key, value := range want {
		if got := env[key]; got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
}

func TestLibreFangUsesItsOfficialCredentialProcessIntegration(t *testing.T) {
	tool, _ := Lookup("librefang")
	if !tool.Native {
		t.Fatal("LibreFang must resolve EveryAPI through its own credential process")
	}
	// `start` is LibreFang's documented daemon launch: it detaches and returns the terminal. Never pin it to the foreground — that turns a launch into an unattended log stream and makes Ctrl+C stop the daemon.
	if got := tool.DefaultArgs; !reflect.DeepEqual(got, []string{"start"}) {
		t.Fatalf("DefaultArgs = %v, want [start]", got)
	}
	if env := tool.Env("https://api.everyapi.ai", "must-not-leak"); len(env) != 0 {
		t.Fatalf("LibreFang native launch received gateway material: %v", env)
	}
}

// TestEnv_Grok verifies Grok Build's documented environment contract and keeps its browser-login state separate from EveryAPI's relay credential.
func TestEnv_Grok(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tool, err := Lookup("grok")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "grok" {
		t.Errorf("ExecName = %q, want grok", tool.ExecName)
	}
	if tool.InstallCmd != "npm install -g @xai-official/grok || npm install -g @xai-official/grok --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @xai-official/grok --registry=https://registry.npmmirror.com" {
		t.Errorf("InstallCmd = %q", tool.InstallCmd)
	}
	if tool.YoloFlag != "--always-approve" {
		t.Errorf("YoloFlag = %q, want --always-approve", tool.YoloFlag)
	}
	if tool.RequiredEndpoint != "openai" {
		t.Errorf("RequiredEndpoint = %q, want openai", tool.RequiredEndpoint)
	}

	env := tool.Env("https://api.everyapi.ai/", "my-token")
	wantBase := "https://api.everyapi.ai/v1"
	if got := env["GROK_MODELS_BASE_URL"]; got != wantBase {
		t.Errorf("GROK_MODELS_BASE_URL = %q, want %q", got, wantBase)
	}
	if _, ok := env["GROK_XAI_API_BASE_URL"]; ok {
		t.Error("GROK_XAI_API_BASE_URL is not read by Grok Build and must not be injected")
	}
	if got := env["XAI_API_KEY"]; got != "my-token" {
		t.Errorf("XAI_API_KEY = %q, want my-token", got)
	}

	wantHome := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "everyapi", "grok-home")
	if err := os.MkdirAll(wantHome, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(wantHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("[auth]\npreferred_method = \"browser\"\n\n[models]\ndefault = \"gateway-chat\"\n\n[ui]\ncompact_mode = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wantHome, "auth.json"), []byte(`{"cached":"xai-oauth"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Grok 1.0.3 no longer honors GROK_AUTH_PATH. Its cached OAuth session outranks XAI_API_KEY, so EveryAPI's dedicated home must begin without it.
	extra, err := tool.Prepare("https://api.everyapi.ai", "my-token")
	if err != nil {
		t.Fatal(err)
	}
	if cleanup := TakePreparedCleanup(extra); cleanup != nil {
		t.Fatal("Grok should not create a temporary auth home")
	}
	if got := extra["GROK_HOME"]; got != wantHome {
		t.Errorf("GROK_HOME = %q, want %q", got, wantHome)
	}
	if info, err := os.Stat(wantHome); err != nil {
		t.Fatalf("stat GROK_HOME: %v", err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("GROK_HOME permissions = %o, want 0700", info.Mode().Perm())
	}
	if _, exists := extra["GROK_AUTH_PATH"]; exists {
		t.Fatal("GROK_AUTH_PATH is ignored by Grok 1.0.3 and must not be injected")
	}
	if _, err := os.Stat(filepath.Join(wantHome, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("EveryAPI Grok OAuth cache survived preparation: %v", err)
	}
	var preparedConfig struct {
		Auth struct {
			PreferredMethod string `toml:"preferred_method"`
		} `toml:"auth"`
		Models struct {
			Default string `toml:"default"`
		} `toml:"models"`
		UI struct {
			CompactMode bool `toml:"compact_mode"`
		} `toml:"ui"`
	}
	if _, err := toml.DecodeFile(configPath, &preparedConfig); err != nil {
		t.Fatalf("decode prepared Grok config: %v", err)
	}
	if got := preparedConfig.Auth.PreferredMethod; got != "api_key" {
		t.Fatalf("auth.preferred_method = %q, want api_key", got)
	}
	if preparedConfig.Models.Default != "gateway-chat" || !preparedConfig.UI.CompactMode {
		t.Fatalf("preparation lost Grok preferences: %+v", preparedConfig)
	}

	second, err := tool.Prepare("https://api.everyapi.ai", "replacement-token")
	if err != nil {
		t.Fatal(err)
	}
	if cleanup := TakePreparedCleanup(second); cleanup != nil {
		t.Fatal("concurrent Grok preparation should not create a temporary auth home")
	}
	if _, err := os.Stat(wantHome); err != nil {
		t.Fatalf("persistent GROK_HOME was removed by auth cleanup: %v", err)
	}
}

func TestGrokPrepareRejectsModelConfigThatWouldOverrideEveryAPIRouting(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	grokHome := filepath.Join(configRoot, "everyapi", "grok-home")
	if err := os.MkdirAll(grokHome, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(grokHome, "config.toml")
	configBody := []byte("[ui]\ncompact_mode = true\n\n[model.gateway-chat]\napi_key = \"personal-key\"\nbase_url = \"https://other.example/v1\"\n")
	if err := os.WriteFile(configPath, configBody, 0o600); err != nil {
		t.Fatal(err)
	}
	tool, err := Lookup("grok")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.PrepareWithModels(
		"https://api.everyapi.ai",
		"relay-key",
		[]Model{{ID: "gateway-chat", SupportedEndpointTypes: []string{"openai"}}},
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "gateway-chat") {
		t.Fatalf("PrepareWithModels error = %v, want named model override rejection", err)
	}
	got, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, configBody) {
		t.Fatal("Grok model conflict check modified the user's config")
	}
	if matches, _ := filepath.Glob(filepath.Join(configRoot, "everyapi", "sessions", "grok-auth-*")); len(matches) != 0 {
		t.Fatalf("failed preparation leaked auth directories: %v", matches)
	}
}

func TestEnv_QwenCode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tool, err := Lookup("qwen-code")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "qwen" {
		t.Errorf("ExecName = %q, want qwen", tool.ExecName)
	}
	if tool.InstallCmd != "npm install -g @qwen-code/qwen-code@latest || npm install -g @qwen-code/qwen-code@latest --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @qwen-code/qwen-code@latest --registry=https://registry.npmmirror.com" {
		t.Errorf("InstallCmd = %q", tool.InstallCmd)
	}
	if tool.YoloFlag != "--yolo" || tool.ModelEnv != "OPENAI_MODEL" || tool.RequiredEndpoint != "openai" {
		t.Errorf("qwen wiring = yolo:%q model:%q endpoint:%q", tool.YoloFlag, tool.ModelEnv, tool.RequiredEndpoint)
	}
	env := tool.Env("https://api.everyapi.ai/", "my-token")
	if env["OPENAI_API_KEY"] != "my-token" || env["OPENAI_BASE_URL"] != "https://api.everyapi.ai/v1" {
		t.Errorf("qwen env = %#v", env)
	}
	wantHome := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "everyapi", "qwen-home")
	if err := os.MkdirAll(wantHome, 0o700); err != nil {
		t.Fatal(err)
	}
	wantSettings := []byte(`{"theme":"existing-user-choice"}`)
	if err := os.WriteFile(filepath.Join(wantHome, "settings.json"), wantSettings, 0o600); err != nil {
		t.Fatal(err)
	}
	extra, err := tool.Prepare("https://api.everyapi.ai", "my-token")
	if err != nil {
		t.Fatal(err)
	}
	if extra["QWEN_HOME"] != wantHome {
		t.Errorf("QWEN_HOME = %q, want %q", extra["QWEN_HOME"], wantHome)
	}
	body, err := os.ReadFile(filepath.Join(wantHome, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, wantSettings) {
		t.Errorf("Prepare overwrote existing settings.json: got %s, want %s", body, wantSettings)
	}
}

func TestEnv_KimiCode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tool, err := Lookup("kimi-code")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "kimi" {
		t.Errorf("ExecName = %q, want kimi", tool.ExecName)
	}
	if tool.InstallCmd != "npm install -g @moonshot-ai/kimi-code || npm install -g @moonshot-ai/kimi-code --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @moonshot-ai/kimi-code --registry=https://registry.npmmirror.com" {
		t.Errorf("InstallCmd = %q", tool.InstallCmd)
	}
	if tool.YoloFlag != "--yolo" || tool.ModelEnv != "KIMI_MODEL_NAME" || tool.RequiredEndpoint != "openai" {
		t.Errorf("kimi wiring = yolo:%q model:%q endpoint:%q", tool.YoloFlag, tool.ModelEnv, tool.RequiredEndpoint)
	}
	env := tool.Env("https://api.everyapi.ai/", "my-token")
	if env["KIMI_MODEL_API_KEY"] != "my-token" || env["KIMI_MODEL_BASE_URL"] != "https://api.everyapi.ai/v1" || env["KIMI_MODEL_PROVIDER_TYPE"] != "openai" {
		t.Errorf("kimi env = %#v", env)
	}
	extra, err := tool.Prepare("https://api.everyapi.ai", "my-token")
	if err != nil {
		t.Fatal(err)
	}
	wantHome := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "everyapi", "kimi-code-home")
	if extra["KIMI_CODE_HOME"] != wantHome {
		t.Errorf("KIMI_CODE_HOME = %q, want %q", extra["KIMI_CODE_HOME"], wantHome)
	}
}

// TestEnv_Hermes verifies hermes' env contract: envFn sets NOTHING. All routing (base_url + relay key) goes through the generated config.yaml in prepareHermes; in particular OPENAI_BASE_URL / OPENAI_API_KEY must NOT be set, since hermes ignores OPENAI_BASE_URL for its main model and only honors OPENAI_API_KEY for openai.com hosts — setting them would create confusing "env is set but nothing routes" debug sessions. HERMES_HOME comes from prepareFn, not here.
func TestEnv_Hermes(t *testing.T) {
	tool, _ := Lookup("hermes")
	env := tool.Env("https://api.everyapi.ai", "my-token")
	if len(env) != 0 {
		t.Errorf("hermes Env should be empty (routing is config-file driven), got %v", env)
	}
}

func TestTransparentEnvUsesOfficialOriginsWithoutRelayCredential(t *testing.T) {
	t.Parallel()

	proxyURL := "http://127.0.0.1:43123"
	caPath := "/tmp/everyapi-connector-ca.pem"
	cases := []struct {
		name      string
		want      map[string]string
		wantUnset []string
	}{
		{
			name: "claude",
			want: map[string]string{
				"ANTHROPIC_BASE_URL":                         "https://api.anthropic.com",
				"ANTHROPIC_AUTH_TOKEN":                       "everyapi-local-connector",
				"NODE_EXTRA_CA_CERTS":                        caPath,
				"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
				"CLAUDE_CODE_USE_GATEWAY":                    "1",
				"CLAUDE_CODE_DISABLE_ADVISOR_TOOL":           "1",
			},
			wantUnset: []string{"ANTHROPIC_API_KEY"},
		},
		{
			name: "codex",
			want: map[string]string{
				"OPENAI_API_KEY":       "everyapi-local-connector",
				"CODEX_CA_CERTIFICATE": caPath,
			},
			wantUnset: []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tool, err := Lookup(c.name)
			if err != nil {
				t.Fatal(err)
			}
			env, unset, err := tool.TransparentEnv(proxyURL, caPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"HTTPS_PROXY", "https_proxy"} {
				if got := env[key]; got != proxyURL {
					t.Errorf("%s = %q, want %q", key, got, proxyURL)
				}
			}
			for key, want := range c.want {
				if got := env[key]; got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			for _, forbidden := range []string{"relay-key", "https://api.everyapi.ai"} {
				for key, value := range env {
					if strings.Contains(value, forbidden) {
						t.Errorf("%s leaks forbidden value %q", key, value)
					}
				}
			}
			for _, key := range c.wantUnset {
				if !containsString(unset, key) {
					t.Errorf("unset = %v, missing %s", unset, key)
				}
			}
			for _, key := range []string{"HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
				if !containsString(unset, key) {
					t.Errorf("unset = %v, missing ambient proxy variable %s", unset, key)
				}
			}
			if !containsString(unset, "EVERYAPI_RELAY_KEY") {
				t.Errorf("unset = %v, missing EVERYAPI_RELAY_KEY", unset)
			}
		})
	}
}

func TestTransparentEnvRejectsUnsupportedTool(t *testing.T) {
	t.Parallel()
	tool, err := Lookup("hermes")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tool.TransparentEnv("http://127.0.0.1:1", "/tmp/ca.pem"); err == nil {
		t.Fatal("TransparentEnv(hermes) unexpectedly succeeded")
	}
}

func TestSupportsTransparentMatchesVerifiedTools(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"claude", "codex"} {
		tool, _ := Lookup(name)
		if !tool.SupportsTransparent() {
			t.Errorf("%s should support transparent mode", name)
		}
	}
	for _, name := range []string{"gemini", "antigravity", "aider", "goose", "crush", "cline", "openclaw", "continue", "kilo", "pi", "pi-web", "vibe", "copilot", "droid", "openhands", "forge", "llxprt", "grok", "qwen-code", "kimi-code", "hermes", "librefang"} {
		tool, _ := Lookup(name)
		if tool.SupportsTransparent() {
			t.Errorf("%s should not advertise transparent mode", name)
		}
	}
}

func TestTransparentEnvReturnsStableUnsetList(t *testing.T) {
	t.Parallel()
	tool, _ := Lookup("codex")
	_, got, err := tool.TransparentEnv("http://127.0.0.1:1", "/tmp/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := []string{"HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"}
	if !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("unset prefix = %v, want %v", got[:len(wantPrefix)], wantPrefix)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestLookup_Unknown returns an error listing supported names so the CLI doesn't have to maintain a parallel list.
func TestLookup_Unknown(t *testing.T) {
	_, err := Lookup("vibes-cli")
	if err == nil {
		t.Fatal("want error for unknown tool")
	}
	msg := err.Error()
	for _, want := range Names() {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %q", want, msg)
		}
	}
}

// TestLookup_CaseInsensitive — users typing `everyapi use Claude` shouldn't get a "not found" surprise.
func TestLookup_CaseInsensitive(t *testing.T) {
	if _, err := Lookup("Claude"); err != nil {
		t.Errorf("Lookup(\"Claude\") error: %v", err)
	}
}

// TestMergeEnv asserts the env-overlay semantics relied on by syscall.Exec: keys in `set` override matching entries from the parent env; non-matching parent entries pass through; keys in `set` not present in the parent are appended.
func TestMergeEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://old.example")
	t.Setenv("UNRELATED_VAR", "keep-me")
	merged := mergeEnv(map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.everyapi.ai",
		"ANTHROPIC_AUTH_TOKEN": "new-tok",
	})
	got := map[string]string{}
	for _, kv := range merged {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			got[kv[:i]] = kv[i+1:]
		}
	}
	if got["ANTHROPIC_BASE_URL"] != "https://api.everyapi.ai" {
		t.Errorf("override missed: ANTHROPIC_BASE_URL=%q", got["ANTHROPIC_BASE_URL"])
	}
	if got["UNRELATED_VAR"] != "keep-me" {
		t.Errorf("passthrough missed: UNRELATED_VAR=%q", got["UNRELATED_VAR"])
	}
	if got["ANTHROPIC_AUTH_TOKEN"] != "new-tok" {
		t.Errorf("append missed: ANTHROPIC_AUTH_TOKEN=%q", got["ANTHROPIC_AUTH_TOKEN"])
	}
	// Sanity: no duplicate keys (a buggy overlay would emit both the parent and the override entries).
	seen := map[string]int{}
	for _, kv := range merged {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			seen[kv[:i]]++
		}
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("duplicate env entry %q (count=%d)", k, n)
		}
	}
}

func TestMergeEnvRemovingDropsAmbientValues(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://ambient.example")
	t.Setenv("NO_PROXY", "api.anthropic.com")
	t.Setenv("KEEP_ME", "yes")
	merged := mergeEnvRemoving(
		map[string]string{"HTTPS_PROXY": "http://127.0.0.1:43123"},
		[]string{"ANTHROPIC_BASE_URL", "NO_PROXY"},
	)
	got := map[string]string{}
	for _, kv := range merged {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			got[kv[:i]] = kv[i+1:]
		}
	}
	if _, ok := got["ANTHROPIC_BASE_URL"]; ok {
		t.Error("ANTHROPIC_BASE_URL was not removed")
	}
	if _, ok := got["NO_PROXY"]; ok {
		t.Error("NO_PROXY was not removed")
	}
	if got["KEEP_ME"] != "yes" || got["HTTPS_PROXY"] != "http://127.0.0.1:43123" {
		t.Fatalf("merged environment lost required values: %v", got)
	}
}
