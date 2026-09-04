package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// codexTestHome redirects ConfigDir() at a fresh tmp dir for one test, by hijacking XDG_CONFIG_HOME (which the SDK's ConfigDir honors first). Returns the resolved CODEX_HOME prepareCodex should produce so the test can assert paths without re-computing the join.
func codexTestHome(t *testing.T) (xdg, wantCodexHome string) {
	t.Helper()
	xdg = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// A launcher-selected effort intentionally overrides persisted defaults in
	// production. Tests that exercise persistence must not inherit the parent
	// developer session's launch-only selection; individual override tests set
	// their own value after calling this helper.
	t.Setenv(ReasoningLevelEnv, "")
	// On non-Linux ConfigDir also checks HOME, but XDG_CONFIG_HOME wins when set — so this works cross-platform.
	return xdg, filepath.Join(xdg, "everyapi", "codex-home")
}

func preparedCodexConfigPath(t *testing.T, env map[string]string) string {
	t.Helper()
	var args []string
	if err := json.Unmarshal([]byte(env[preparedArgvMarker]), &args); err != nil {
		t.Fatalf("decode prepared Codex arguments: %v", err)
	}
	if len(args) != 2 || args[0] != "--profile" || args[1] == "" {
		t.Fatalf("prepared Codex arguments = %v, want [--profile <name>]", args)
	}
	return filepath.Join(env["CODEX_HOME"], args[1]+".config.toml")
}

func assertTreeDoesNotContain(t *testing.T, root, forbidden string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), forbidden) {
			t.Errorf("%s contains forbidden launch credential", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestPrepareCodex_WritesFiles verifies the apikey-mode auth.json and the everyapi-provider config.toml both land in CODEX_HOME with the expected schema. This is the smoke test: if it breaks, `everyapi use codex` won't route through the gateway.
func TestPrepareCodex_WritesFiles(t *testing.T) {
	_, codexHome := codexTestHome(t)

	extra, err := prepareCodex("https://api.everyapi.ai", "sk-everyapi-abc")
	if err != nil {
		t.Fatalf("prepareCodex error: %v", err)
	}
	if got := extra["CODEX_HOME"]; got != codexHome {
		t.Errorf("CODEX_HOME env = %q, want %q", got, codexHome)
	}

	// auth.json: apikey mode, launch-independent placeholder present, tokens nulled.
	authBody, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var auth map[string]any
	if err := json.Unmarshal(authBody, &auth); err != nil {
		t.Fatalf("parse auth.json: %v\n%s", err, authBody)
	}
	if auth["auth_mode"] != "apikey" {
		t.Errorf("auth_mode = %v, want \"apikey\"", auth["auth_mode"])
	}
	if auth["OPENAI_API_KEY"] != transparentPlaceholderCredential {
		t.Errorf("OPENAI_API_KEY = %v, want connector placeholder", auth["OPENAI_API_KEY"])
	}
	if strings.Contains(string(authBody), "sk-everyapi-abc") {
		t.Fatal("auth.json persisted the relay key")
	}
	if auth["tokens"] != nil {
		t.Errorf("tokens = %v, want nil (chatgpt tokens must be cleared in apikey mode)", auth["tokens"])
	}

	// config.toml: must wire base_url + select everyapi provider.
	cfgBody, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	cfg := string(cfgBody)
	for _, want := range []string{
		`model_provider = "everyapi"`,
		`[model_providers.everyapi]`,
		`base_url = "https://api.everyapi.ai/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		// Pins the routing surface: omitting wire_api falls back to codex's Chat default (/v1/chat/completions) instead of the gateway's native /v1/responses.
		`wire_api = "responses"`,
		`http_headers = { "X-EveryAPI-Agent" = "codex" }`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.toml missing %q\nFull config:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "X-EveryAPI-Session") {
		t.Errorf("config.toml must not fabricate a logical session identifier:\n%s", cfg)
	}
	var generated struct {
		ModelProviders map[string]struct {
			HTTPHeaders map[string]string `toml:"http_headers"`
		} `toml:"model_providers"`
	}
	if _, err := toml.Decode(cfg, &generated); err != nil {
		t.Fatalf("generated config.toml is invalid: %v", err)
	}
	if got := generated.ModelProviders["everyapi"].HTTPHeaders["X-EveryAPI-Agent"]; got != "codex" {
		t.Errorf("EveryAPI provider agent header = %q, want codex", got)
	}
}

func TestPrepareCodexAddsArtifactStandardAndOnlyCurrentTmuxContext(t *testing.T) {
	_, codexHome := codexTestHome(t)
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv(TerminalModeEnvironment, "tmux")
	t.Setenv(TmuxSessionEnvironment, "everyapi-123-456")
	t.Setenv(TmuxAttachCommandEnvironment, "tmux attach -t everyapi-123-456")
	if _, err := prepareCodex("https://api.everyapi.ai", "token"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "You are running inside tmux session everyapi-123-456") {
		t.Fatalf("Codex config missing tmux developer instructions:\n%s", body)
	}
	if !strings.Contains(string(body), "EveryAPI Artifact delivery standard") {
		t.Fatalf("Codex config missing artifact delivery standard:\n%s", body)
	}
	// The capability list travels the same developer_instructions path; assert it here too, because AgentInstructions growing a section that never reaches Codex is a silent regression.
	if !strings.Contains(string(body), "EveryAPI CLI") {
		t.Fatalf("Codex config missing the capability list:\n%s", body)
	}
	if !strings.Contains(string(body), "EveryAPI Computer Use") || !strings.Contains(string(body), "computer get-app-state") {
		t.Fatalf("Codex config missing computer-use instructions:\n%s", body)
	}

	t.Setenv("TMUX", "")
	t.Setenv(TerminalModeEnvironment, "native")
	if _, err := prepareCodex("https://api.everyapi.ai", "token"); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "developer_instructions") || !strings.Contains(string(body), "EveryAPI Artifact delivery standard") {
		t.Fatalf("Codex native config lost artifact delivery instructions:\n%s", body)
	}
	if strings.Contains(string(body), "You are running inside tmux session") {
		t.Fatalf("Codex native config retained stale tmux instructions:\n%s", body)
	}
}

func TestPrepareCodexTransparentUsesOfficialHTTPProviderAndPlaceholder(t *testing.T) {
	_, codexHome := codexTestHome(t)
	tool, _ := Lookup("codex")

	extra, err := tool.PrepareTransparent()
	if err != nil {
		t.Fatalf("PrepareTransparent: %v", err)
	}
	if got := extra["CODEX_HOME"]; got != codexHome {
		t.Fatalf("CODEX_HOME = %q, want %q", got, codexHome)
	}
	authBody, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]any
	if err := json.Unmarshal(authBody, &auth); err != nil {
		t.Fatal(err)
	}
	if got := auth["OPENAI_API_KEY"]; got != transparentPlaceholderCredential {
		t.Errorf("OPENAI_API_KEY = %v, want connector placeholder", got)
	}
	configBody, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	configText := string(configBody)
	for _, required := range []string{
		`model_provider = "everyapi_openai"`,
		`[model_providers.everyapi_openai]`,
		`base_url = "https://api.openai.com/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
		`requires_openai_auth = false`,
		`supports_websockets = false`,
		`supports_standalone_web_search = true`,
	} {
		if !strings.Contains(configText, required) {
			t.Errorf("transparent config.toml missing %q:\n%s", required, configText)
		}
	}
	for _, forbidden := range []string{"api.everyapi", "openai_base_url"} {
		if strings.Contains(configText, forbidden) {
			t.Errorf("config.toml contains transparent-mode forbidden value %q:\n%s", forbidden, configText)
		}
	}
}

func TestPrepareCodexTransparentPreservesUserModelDefaults(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("create codex home: %v", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	const userDefaults = "model = \"gpt-5.6-terra\"\nmodel_reasoning_effort = \"high\"\n"
	if err := os.WriteFile(configPath, []byte(userDefaults), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	if _, err := prepareCodexTransparent(); err != nil {
		t.Fatalf("prepareCodexTransparent error: %v", err)
	}
	configBody, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(configBody)
	for _, want := range []string{
		`model_provider = "everyapi_openai"`,
		`model = "gpt-5.6-terra"`,
		`model_reasoning_effort = "high"`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config.toml missing preserved default %q\nFull config:\n%s", want, config)
		}
	}
}

func TestPrepareCodex_PreservesUserModelDefaults(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("create codex home: %v", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	const userDefaults = "model = \"user-selected-model\"\nmodel_reasoning_effort = \"high\"\n"
	if err := os.WriteFile(configPath, []byte(userDefaults), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	if _, err := prepareCodex("https://api.everyapi.ai", "tok"); err != nil {
		t.Fatalf("prepareCodex error: %v", err)
	}
	configBody, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(configBody)
	for _, want := range []string{
		`model = "user-selected-model"`,
		`model_reasoning_effort = "high"`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config.toml missing preserved default %q\nFull config:\n%s", want, config)
		}
	}
}

func TestPrepareCodex_PreservesExistingConfigOnParseError(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("create codex home: %v", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	const invalidConfig = "model = \"unterminated\n"
	if err := os.WriteFile(configPath, []byte(invalidConfig), 0o644); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	if _, err := prepareCodex("https://api.everyapi.ai", "tok"); err == nil {
		t.Fatal("prepareCodex succeeded with an invalid existing config")
	}
	configBody, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if got := string(configBody); got != invalidConfig {
		t.Errorf("config.toml was overwritten after a parse error\ngot:  %q\nwant: %q", got, invalidConfig)
	}
}

func TestPrepareCodex_PreservesEscapedModelDefault(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("create codex home: %v", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	const userDefaults = "model = \"\\b\"\n"
	if err := os.WriteFile(configPath, []byte(userDefaults), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	if _, err := prepareCodex("https://api.everyapi.ai", "tok"); err != nil {
		t.Fatalf("prepareCodex error: %v", err)
	}
	configBody, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var defaults codexUserDefaults
	if _, err := toml.Decode(string(configBody), &defaults); err != nil {
		t.Fatalf("generated config is not valid TOML: %v", err)
	}
	if defaults.Model != "\b" {
		t.Errorf("model = %q, want backspace", defaults.Model)
	}
}

// TestPrepareCodex_TrailingSlashBase pins the joinBase invariant for codex's config.toml path: a dev-style `http://localhost:8787/` must not produce `http://localhost:8787//v1`. Migrated from the codex envFn test (envFn no longer touches base_url).
func TestPrepareCodex_TrailingSlashBase(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if _, err := prepareCodex("http://localhost:8787/", "tok"); err != nil {
		t.Fatalf("prepareCodex error: %v", err)
	}
	cfgBody, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	cfg := string(cfgBody)
	if !strings.Contains(cfg, `base_url = "http://localhost:8787/v1"`) {
		t.Errorf("expected single-slash join in base_url, got config:\n%s", cfg)
	}
	if strings.Contains(cfg, "//v1") {
		t.Error("found double slash in base_url")
	}
}

// TestPrepareCodex_FilePerms guards Codex's credential boundary. auth.json currently carries only a placeholder, but keep the vendor-compatible 0600 mode so future schema changes cannot silently broaden access. config.toml holds no secret and inherits 0644 (readable to the user's tools that might inspect it, like a debug helper). Skipped on Windows where Unix perms don't apply.
func TestPrepareCodex_FilePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits not meaningful on Windows")
	}
	_, codexHome := codexTestHome(t)
	if _, err := prepareCodex("https://api.everyapi.ai", "tok"); err != nil {
		t.Fatalf("prepareCodex error: %v", err)
	}
	info, err := os.Stat(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("auth.json perm = %o, want vendor-compatible 0600", perm)
	}
	dirInfo, err := os.Stat(codexHome)
	if err != nil {
		t.Fatalf("stat codex-home: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("codex-home perm = %o, want 0700", perm)
	}
}

// TestPrepareCodexDoesNotPersistRotatedKeys re-runs on the same directory and verifies neither launch credential reaches persistent auth state. The real key comes from Tool.Env for each child; auth.json exists only to pin API-key mode with a launch-independent placeholder.
func TestPrepareCodexDoesNotPersistRotatedKeys(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if _, err := prepareCodex("https://api.everyapi.ai", "first-key"); err != nil {
		t.Fatalf("first prepareCodex: %v", err)
	}
	if _, err := prepareCodex("https://api.everyapi.ai", "second-key"); err != nil {
		t.Fatalf("second prepareCodex: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var auth map[string]any
	if err := json.Unmarshal(body, &auth); err != nil {
		t.Fatalf("parse auth.json: %v", err)
	}
	if auth["OPENAI_API_KEY"] != transparentPlaceholderCredential {
		t.Errorf("OPENAI_API_KEY after rerun = %v, want connector placeholder", auth["OPENAI_API_KEY"])
	}
	if strings.Contains(string(body), "first-key") || strings.Contains(string(body), "second-key") {
		t.Fatal("persistent auth.json contains a rotated relay key")
	}
}

// TestCodexTool_PrepareWired makes sure the Registry entry actually invokes prepareCodex — a refactor that drops prepareFn from the codex entry would leave the function existing-but-unused and the CLI silently regressing to ChatGPT login. Catching that here is cheaper than diagnosing it post-release.
func TestCodexTool_PrepareWired(t *testing.T) {
	_, codexHome := codexTestHome(t)
	tool, _ := Lookup("codex")
	extra, err := tool.Prepare("https://api.everyapi.ai", "tok")
	if err != nil {
		t.Fatalf("tool.Prepare error: %v", err)
	}
	if extra["CODEX_HOME"] != codexHome {
		t.Errorf("codex tool.Prepare didn't run prepareCodex (CODEX_HOME=%q)", extra["CODEX_HOME"])
	}
}

func TestPrepareCodexWithModelsWritesPickerCatalog(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	original := codexBundledCatalog
	codexBundledCatalog = func() ([]byte, error) {
		return []byte(`{"models":[{"slug":"gpt-template","display_name":"Template","description":"template","default_reasoning_level":null,"supported_reasoning_levels":[],"shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,"availability_nux":null,"upgrade":null,"base_instructions":"You are a coding agent.","support_verbosity":false,"default_verbosity":null,"apply_patch_tool_type":"freeform","truncation_policy":{"mode":"bytes","limit":10000},"supports_parallel_tool_calls":true,"experimental_supported_tools":[]}]}`), nil
	}
	t.Cleanup(func() { codexBundledCatalog = original })
	tool, _ := Lookup("codex")
	extra, err := tool.PrepareWithModels("https://api.everyapi.ai", "tok", testLaunchCatalog[:1], "")
	if err != nil {
		t.Fatal(err)
	}
	preparedHome := extra[preparedHomeMarker]
	configPath := preparedCodexConfigPath(t, extra)
	args := TakePreparedArgs(extra)
	if len(args) != 2 || args[0] != "--profile" || args[1]+".config.toml" != filepath.Base(configPath) {
		t.Fatalf("prepared Codex arguments = %v, want profile for %s", args, configPath)
	}
	defer TakePreparedCleanup(extra)()
	configBody, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configBody), `model_catalog_json = "`) {
		t.Fatalf("Codex config missing model_catalog_json: %s", configBody)
	}
	catalogBody, err := os.ReadFile(filepath.Join(preparedHome, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(catalogBody), `"slug": "gpt-5.6-terra"`) {
		t.Fatalf("Codex catalog missing relay model: %s", catalogBody)
	}
}

func TestPrepareCodexWithModelsPersistsSessionsInStableHome(t *testing.T) {
	_, persistentHome := codexTestHome(t)
	stubCodexBundledCatalog(t)
	models := []Model{{ID: "gpt-5.6-sol"}}

	first, err := prepareCodexWithModels(
		"https://api.everyapi.ai",
		"first-key",
		models,
		"gpt-5.6-sol",
	)
	if err != nil {
		t.Fatalf("first prepareCodexWithModels: %v", err)
	}
	firstPreparedHome := first[preparedHomeMarker]
	firstCleanup := TakePreparedCleanup(first)
	defer firstCleanup()
	firstHome := first["CODEX_HOME"]
	if firstHome != persistentHome {
		t.Fatalf("CODEX_HOME = %q, want persistent home %q", firstHome, persistentHome)
	}
	firstProfile := preparedCodexConfigPath(t, first)
	if got := first["CODEX_SQLITE_HOME"]; got != "" {
		t.Fatalf("CODEX_SQLITE_HOME = %q, want SQLite state under CODEX_HOME", got)
	}
	authBody, err := os.ReadFile(filepath.Join(firstHome, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(authBody), "first-key") || !strings.Contains(string(authBody), transparentPlaceholderCredential) {
		t.Fatalf("persistent auth must contain only the launch-independent placeholder:\n%s", authBody)
	}
	assertTreeDoesNotContain(t, firstHome, "first-key")
	assertTreeDoesNotContain(t, firstPreparedHome, "first-key")

	sessionPath := filepath.Join(firstHome, "sessions", "2026", "08", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionPath, []byte("session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(firstHome, "session_index.jsonl")
	replacementPath := indexPath + ".tmp"
	const namedEntry = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"named-session","updated_at":"2026-08-09T00:00:00Z"}` + "\n"
	if err := os.WriteFile(replacementPath, []byte(namedEntry), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, indexPath); err != nil {
		t.Fatal(err)
	}
	second, err := prepareCodexWithModels(
		"https://api.everyapi.ai",
		"second-key",
		[]Model{{ID: "gpt-5.6-terra"}},
		"gpt-5.6-terra",
	)
	if err != nil {
		t.Fatalf("second prepareCodexWithModels: %v", err)
	}
	defer TakePreparedCleanup(second)()
	if second["CODEX_HOME"] != firstHome {
		t.Fatalf("second CODEX_HOME = %q, want stable home %q", second["CODEX_HOME"], firstHome)
	}
	secondProfile := preparedCodexConfigPath(t, second)
	if secondProfile == firstProfile {
		t.Fatalf("launches shared prepared profile %q", firstProfile)
	}
	if body, err := os.ReadFile(firstProfile); err != nil {
		t.Fatalf("first profile disappeared while its launch was active: %v", err)
	} else if !strings.Contains(string(body), `model = "gpt-5.6-sol"`) {
		t.Fatalf("first active profile lost its model: %s", body)
	}
	if body, err := os.ReadFile(secondProfile); err != nil {
		t.Fatalf("read second active profile: %v", err)
	} else if !strings.Contains(string(body), `model = "gpt-5.6-terra"`) {
		t.Fatalf("second active profile lost its model: %s", body)
	}
	firstCleanup()
	if _, err := os.Stat(firstProfile); !os.IsNotExist(err) {
		t.Fatalf("first launch profile survived cleanup: %v", err)
	}
	if _, err := os.Stat(secondProfile); err != nil {
		t.Fatalf("cleaning first launch removed second active profile: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(second["CODEX_HOME"], "sessions", "2026", "08", "session.jsonl")); err != nil {
		t.Fatalf("read session from second launch: %v", err)
	} else if string(body) != "session\n" {
		t.Fatalf("persisted session = %q, want %q", body, "session\\n")
	}
	if body, err := os.ReadFile(filepath.Join(second["CODEX_HOME"], "session_index.jsonl")); err != nil {
		t.Fatalf("read named-session index from second launch: %v", err)
	} else if string(body) != namedEntry {
		t.Fatalf("persisted session index = %q, want %q", body, namedEntry)
	}
}

func TestPrepareCodexWithModelsKeepsCanonicalRolloutsInsideCodexHome(t *testing.T) {
	codexTestHome(t)
	stubCodexBundledCatalog(t)

	env, err := prepareCodexWithModels(
		"https://api.everyapi.ai",
		"key",
		[]Model{{ID: "gpt-5.6-sol"}},
		"gpt-5.6-sol",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer TakePreparedCleanup(env)()

	rolloutPath := filepath.Join(env["CODEX_HOME"], "sessions", "2026", "08", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolloutPath, []byte("rollout\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	canonicalHome, err := filepath.EvalSymlinks(env["CODEX_HOME"])
	if err != nil {
		t.Fatal(err)
	}
	canonicalRollout, err := filepath.EvalSymlinks(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(canonicalHome, canonicalRollout)
	if err != nil {
		t.Fatal(err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("canonical rollout %q escapes CODEX_HOME %q", canonicalRollout, canonicalHome)
	}
}

func TestPrepareCodexTransparentWithModelsUsesPersistentSessionState(t *testing.T) {
	codexTestHome(t)
	stubCodexBundledCatalog(t)

	env, err := prepareCodexTransparentWithModels(
		[]Model{{ID: "gpt-5.6-sol"}},
		"gpt-5.6-sol",
	)
	if err != nil {
		t.Fatalf("prepareCodexTransparentWithModels: %v", err)
	}
	defer TakePreparedCleanup(env)()
	if got := env["CODEX_SQLITE_HOME"]; got != "" {
		t.Fatalf("CODEX_SQLITE_HOME = %q, want SQLite state under CODEX_HOME", got)
	}
	if info, err := os.Stat(filepath.Join(env["CODEX_HOME"], "sessions")); err != nil {
		t.Fatalf("stat persistent sessions directory: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("persistent sessions path is not a directory: %v", info.Mode())
	}
}

func TestPrepareCodexWithModelsCleansLaunchHomeWhenSessionStateSetupFails(t *testing.T) {
	xdg, persistentHome := codexTestHome(t)
	stubCodexBundledCatalog(t)
	if err := os.MkdirAll(persistentHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(persistentHome, "sessions"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := prepareCodexWithModels(
		"https://api.everyapi.ai",
		"key",
		[]Model{{ID: "gpt-5.6-sol"}},
		"gpt-5.6-sol",
	); err == nil {
		t.Fatal("prepareCodexWithModels succeeded with an invalid persistent sessions path")
	}
	launchRoot := filepath.Join(xdg, "everyapi", "sessions")
	entries, err := os.ReadDir(launchRoot)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed preparation left %d temporary Codex home(s) in %s", len(entries), launchRoot)
	}
}

// TestClaudeTool_NoPrepare guards the negative case: claude don't need pre-exec setup, so tool.Prepare must be a clean no-op (nil map, nil err). A future regression that accidentally wires a shared prepareFn would silently create unwanted ~/.config/everyapi dirs for those flows.
func TestClaudeTool_NoPrepare(t *testing.T) {
	for _, name := range []string{"claude"} {
		tool, _ := Lookup(name)
		extra, err := tool.Prepare("https://api.everyapi.ai", "tok")
		if err != nil {
			t.Errorf("%s tool.Prepare error: %v", name, err)
		}
		if extra != nil {
			t.Errorf("%s tool.Prepare returned %v, want nil (no setup needed)", name, extra)
		}
	}
}

// stubCodexBundledCatalog replaces the bundled-metadata read, which otherwise shells out to the real `codex` binary. CI has no codex on PATH, so any test that passes a non-empty model list must stub this or it fails there while passing on a developer machine that happens to have the CLI installed.
func stubCodexBundledCatalog(t *testing.T) {
	t.Helper()
	original := codexBundledCatalog
	codexBundledCatalog = func() ([]byte, error) {
		return []byte(`{"models":[{"slug":"gpt-template","display_name":"Template","description":"template","default_reasoning_level":null,"supported_reasoning_levels":[],"shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,"availability_nux":null,"upgrade":null,"base_instructions":"You are a coding agent.","support_verbosity":false,"default_verbosity":null,"apply_patch_tool_type":"freeform","truncation_policy":{"mode":"bytes","limit":10000},"supports_parallel_tool_calls":true,"experimental_supported_tools":[]}]}`), nil
	}
	t.Cleanup(func() { codexBundledCatalog = original })
}

// TestPrepareCodex_SeedsBootModelIntoAFreshHome covers the gap the "root-level model is preserved" contract cannot fill in a fresh per-launch profile. The selection EveryAPI persisted seeds the boot model instead.
func TestPrepareCodex_SeedsBootModelIntoAFreshHome(t *testing.T) {
	codexTestHome(t)
	stubCodexBundledCatalog(t)
	models := []Model{{ID: "claude-opus-4-8"}, {ID: "ark-doubao-seed"}}

	env, err := prepareCodexWithModels("https://api.everyapi.ai", "tok", models, "claude-opus-4-8")
	if err != nil {
		t.Fatalf("prepareCodexWithModels: %v", err)
	}
	home := env["CODEX_HOME"]
	if home == "" {
		t.Fatal("no CODEX_HOME returned")
	}
	configPath := preparedCodexConfigPath(t, env)
	t.Cleanup(TakePreparedCleanup(env))

	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `model = "claude-opus-4-8"`) {
		t.Fatalf("fresh home did not get the catalogue's first model as its boot model:\n%s", body)
	}
}

// TestPrepareCodex_BootModelDoesNotOverrideAUserChoice keeps the seeding subordinate to the existing contract: a model the user set in a home that survives is still preserved, and the seed only fills an empty field.
func TestPrepareCodex_BootModelDoesNotOverrideAUserChoice(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"user-selected-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No models → legacy fixed home, which is the shape that can carry a user's own config forward.
	if _, err := prepareCodexWithModels("https://api.everyapi.ai", "tok", nil, ""); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `model = "user-selected-model"`) {
		t.Fatalf("the user's own model was overwritten:\n%s", body)
	}
}

// TestPrepareCodexTransparent_SeedsBootModelIntoAFreshHome is the same seeding on the path codex actually takes by default. Transparent mode is the default for codex, so covering only the injected path would leave plain `everyapi use codex` still booting on whatever the catalogue listed first.
func TestPrepareCodexTransparent_SeedsBootModelIntoAFreshHome(t *testing.T) {
	codexTestHome(t)
	stubCodexBundledCatalog(t)
	models := []Model{{ID: "gpt-5.1"}, {ID: "ark-doubao-seed"}}

	env, err := prepareCodexTransparentWithModels(models, "gpt-5.1")
	if err != nil {
		t.Fatalf("prepareCodexTransparentWithModels: %v", err)
	}
	home := env["CODEX_HOME"]
	if home == "" {
		t.Fatal("no CODEX_HOME returned")
	}
	configPath := preparedCodexConfigPath(t, env)
	t.Cleanup(TakePreparedCleanup(env))

	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `model = "gpt-5.1"`) {
		t.Fatalf("transparent fresh home did not get the catalogue's first model:\n%s", body)
	}
}

func TestPrepareCodexTransparent_InheritsReasoningEffortIntoFreshHome(t *testing.T) {
	_, persistentHome := codexTestHome(t)
	stubCodexBundledCatalog(t)
	t.Setenv(ReasoningLevelEnv, "")
	if err := os.MkdirAll(persistentHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(persistentHome, "config.toml"),
		[]byte("model = \"old-model\"\nmodel_reasoning_effort = \"medium\"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	env, err := prepareCodexTransparentWithModels(
		[]Model{{ID: "gpt-5.6-sol"}},
		"gpt-5.6-sol",
	)
	if err != nil {
		t.Fatalf("prepareCodexTransparentWithModels: %v", err)
	}
	configPath := preparedCodexConfigPath(t, env)
	t.Cleanup(TakePreparedCleanup(env))

	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config := string(body)
	if !strings.Contains(config, `model = "gpt-5.6-sol"`) {
		t.Fatalf("fresh home did not get remembered boot model:\n%s", config)
	}
	if !strings.Contains(config, `model_reasoning_effort = "medium"`) {
		t.Fatalf("fresh home lost persistent reasoning effort:\n%s", config)
	}
}

func TestPrepareCodex_InheritsReasoningEffortIntoFreshHome(t *testing.T) {
	_, persistentHome := codexTestHome(t)
	stubCodexBundledCatalog(t)
	t.Setenv(ReasoningLevelEnv, "")
	if err := os.MkdirAll(persistentHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(persistentHome, "config.toml"),
		[]byte("model = \"old-model\"\nmodel_reasoning_effort = \"medium\"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	env, err := prepareCodexWithModels(
		"https://api.everyapi.ai",
		"tok",
		[]Model{{ID: "gpt-5.6-sol"}},
		"gpt-5.6-sol",
	)
	if err != nil {
		t.Fatalf("prepareCodexWithModels: %v", err)
	}
	configPath := preparedCodexConfigPath(t, env)
	t.Cleanup(TakePreparedCleanup(env))

	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config := string(body)
	if !strings.Contains(config, `model = "gpt-5.6-sol"`) {
		t.Fatalf("fresh home did not get remembered boot model:\n%s", config)
	}
	if strings.Contains(config, `model = "old-model"`) {
		t.Fatalf("fresh home inherited stale persistent model:\n%s", config)
	}
	if !strings.Contains(config, `model_reasoning_effort = "medium"`) {
		t.Fatalf("fresh home lost persistent reasoning effort:\n%s", config)
	}
}

// TestPrepareCodex_DoesNotPinAModelTheUserNeverChose is the guard against turning "EveryAPI has no preference" into "EveryAPI pinned position 0". Seeding the boot model from the catalogue's first entry rather than from an actual selection would silently override codex's own built-in default on every launch — a worse outcome than the alphabetical-ordering bug this branch set out to fix, and applied to users who never asked for a model.
func TestPrepareCodex_DoesNotPinAModelTheUserNeverChose(t *testing.T) {
	models := []Model{{ID: "ark-doubao-seed"}, {ID: "gpt-5.1"}}
	for _, tc := range []struct {
		name    string
		prepare func() (map[string]string, error)
	}{
		{"injected", func() (map[string]string, error) {
			return prepareCodexWithModels("https://api.everyapi.ai", "tok", models, "")
		}},
		{"transparent", func() (map[string]string, error) {
			return prepareCodexTransparentWithModels(models, "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codexTestHome(t)
			stubCodexBundledCatalog(t)
			env, err := tc.prepare()
			if err != nil {
				t.Fatal(err)
			}
			configPath := preparedCodexConfigPath(t, env)
			t.Cleanup(TakePreparedCleanup(env))
			body, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			// A root-level `model = ` line, not model_provider or model_reasoning_effort or model_catalog_json.
			for _, line := range strings.Split(string(body), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "model = ") {
					t.Fatalf("codex was pinned to a model with no selection behind it: %q\nFull config:\n%s", line, body)
				}
			}
		})
	}
}

func TestPrepareCodexWithModelsDoesNotLayerAStaleLegacyModel(t *testing.T) {
	_, persistentHome := codexTestHome(t)
	stubCodexBundledCatalog(t)
	t.Setenv(ReasoningLevelEnv, "")
	if err := os.MkdirAll(persistentHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(persistentHome, "config.toml"),
		[]byte("model = \"stale-legacy-model\"\nmodel_reasoning_effort = \"medium\"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	env, err := prepareCodexWithModels(
		"https://api.everyapi.ai",
		"tok",
		[]Model{{ID: "gpt-5.6-sol"}},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	configPath := preparedCodexConfigPath(t, env)
	t.Cleanup(TakePreparedCleanup(env))

	for _, path := range []string{filepath.Join(persistentHome, "config.toml"), configPath} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "model = ") {
				t.Fatalf("stale model survived in %s: %q\n%s", path, line, body)
			}
		}
	}
}
