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

// codexTestHome redirects ConfigDir() at a fresh tmp dir for one
// test, by hijacking XDG_CONFIG_HOME (which the SDK's ConfigDir
// honors first). Returns the resolved CODEX_HOME prepareCodex
// should produce so the test can assert paths without re-computing
// the join.
func codexTestHome(t *testing.T) (xdg, wantCodexHome string) {
	t.Helper()
	xdg = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// On non-Linux ConfigDir also checks HOME, but XDG_CONFIG_HOME
	// wins when set — so this works cross-platform.
	return xdg, filepath.Join(xdg, "everyapi", "codex-home")
}

// TestPrepareCodex_WritesFiles verifies the apikey-mode auth.json
// and the everyapi-provider config.toml both land in CODEX_HOME with
// the expected schema. This is the smoke test: if it breaks,
// `everyapi use codex` won't route through the gateway.
func TestPrepareCodex_WritesFiles(t *testing.T) {
	_, codexHome := codexTestHome(t)

	extra, err := prepareCodex("https://api.everyapi.ai", "sk-everyapi-abc")
	if err != nil {
		t.Fatalf("prepareCodex error: %v", err)
	}
	if got := extra["CODEX_HOME"]; got != codexHome {
		t.Errorf("CODEX_HOME env = %q, want %q", got, codexHome)
	}

	// auth.json: apikey mode, key present, tokens nulled.
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
	if auth["OPENAI_API_KEY"] != "sk-everyapi-abc" {
		t.Errorf("OPENAI_API_KEY = %v, want \"sk-everyapi-abc\"", auth["OPENAI_API_KEY"])
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
		// Pins the routing surface: omitting wire_api falls back to
		// codex's Chat default (/v1/chat/completions) instead of the
		// gateway's native /v1/responses.
		`wire_api = "responses"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.toml missing %q\nFull config:\n%s", want, cfg)
		}
	}
}

func TestPrepareCodexTransparentUsesBuiltInOpenAIProviderAndPlaceholder(t *testing.T) {
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
	if !strings.Contains(configText, `model_provider = "openai"`) {
		t.Errorf("config.toml does not select built-in OpenAI provider:\n%s", configText)
	}
	for _, forbidden := range []string{"api.everyapi", "model_providers.everyapi", "openai_base_url"} {
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
		`model_provider = "openai"`,
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

// TestPrepareCodex_TrailingSlashBase pins the joinBase invariant for
// codex's config.toml path: a dev-style `http://localhost:8787/` must
// not produce `http://localhost:8787//v1`. Migrated from the codex
// envFn test (envFn no longer touches base_url).
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

// TestPrepareCodex_FilePerms guards the secret-bearing files. auth.json
// carries a relay key — chmod 0600 is non-negotiable. config.toml
// holds no secret but inherits 0644 (readable to the user's tools
// that might inspect it, like a debug helper). Skipped on Windows
// where Unix perms don't apply.
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
		t.Errorf("auth.json perm = %o, want 0600 (carries bearer token)", perm)
	}
	dirInfo, err := os.Stat(codexHome)
	if err != nil {
		t.Fatalf("stat codex-home: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("codex-home perm = %o, want 0700", perm)
	}
}

// TestPrepareCodex_Idempotent re-runs on the same directory and
// verifies the new relay key wins on the second call (covers the
// "user rotated keys / switched groups" case). The directory is
// reused on purpose (preserves codex's session/cache state across
// invocations), so the rewrite-every-call contract matters.
func TestPrepareCodex_Idempotent(t *testing.T) {
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
	if auth["OPENAI_API_KEY"] != "second-key" {
		t.Errorf("OPENAI_API_KEY after rerun = %v, want \"second-key\"", auth["OPENAI_API_KEY"])
	}
}

// TestCodexTool_PrepareWired makes sure the Registry entry actually
// invokes prepareCodex — a refactor that drops prepareFn from the
// codex entry would leave the function existing-but-unused and the
// CLI silently regressing to ChatGPT login. Catching that here is
// cheaper than diagnosing it post-release.
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
	defer TakePreparedCleanup(extra)()
	home := extra["CODEX_HOME"]
	configBody, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configBody), `model_catalog_json = "`) {
		t.Fatalf("Codex config missing model_catalog_json: %s", configBody)
	}
	catalogBody, err := os.ReadFile(filepath.Join(home, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(catalogBody), `"slug": "gpt-5.6-terra"`) {
		t.Fatalf("Codex catalog missing relay model: %s", catalogBody)
	}
}

func TestPrepareCodexWithModelsPersistsSessionsAcrossLaunchHomes(t *testing.T) {
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
	firstHome := first["CODEX_HOME"]
	if firstHome == persistentHome {
		t.Fatalf("live-catalog launch reused persistent CODEX_HOME %q", persistentHome)
	}
	if got := first["CODEX_SQLITE_HOME"]; got != "" {
		t.Fatalf("CODEX_SQLITE_HOME = %q, want launch-local SQLite state", got)
	}

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
	TakePreparedCleanup(first)()

	second, err := prepareCodexWithModels(
		"https://api.everyapi.ai",
		"second-key",
		models,
		"gpt-5.6-sol",
	)
	if err != nil {
		t.Fatalf("second prepareCodexWithModels: %v", err)
	}
	defer TakePreparedCleanup(second)()
	if second["CODEX_HOME"] == firstHome {
		t.Fatalf("launches shared temporary CODEX_HOME %q", firstHome)
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

func TestPrepareCodexWithModelsMergesConcurrentSessionIndexUpdates(t *testing.T) {
	codexTestHome(t)
	stubCodexBundledCatalog(t)
	models := []Model{{ID: "gpt-5.6-sol"}}
	prepare := func() map[string]string {
		t.Helper()
		env, err := prepareCodexWithModels(
			"https://api.everyapi.ai",
			"key",
			models,
			"gpt-5.6-sol",
		)
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	replaceIndex := func(env map[string]string, body string) {
		t.Helper()
		path := filepath.Join(env["CODEX_HOME"], "session_index.jsonl")
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}

	first := prepare()
	second := prepare()
	const firstEntry = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"first","updated_at":"2026-08-09T00:00:01Z"}` + "\n"
	const secondEntry = `{"id":"22222222-2222-4222-8222-222222222222","thread_name":"second","updated_at":"2026-08-09T00:00:02Z"}` + "\n"
	replaceIndex(first, firstEntry)
	replaceIndex(second, secondEntry)
	TakePreparedCleanup(first)()
	TakePreparedCleanup(second)()

	third := prepare()
	defer TakePreparedCleanup(third)()
	body, err := os.ReadFile(filepath.Join(third["CODEX_HOME"], "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{firstEntry, secondEntry} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("merged session index missing %q:\n%s", want, body)
		}
	}
}

func TestPrepareCodexWithModelsPreservesStateWhenUpdatedSessionIndexIsCorrupt(t *testing.T) {
	_, persistentHome := codexTestHome(t)
	stubCodexBundledCatalog(t)
	models := []Model{{ID: "gpt-5.6-sol"}}
	prepare := func() map[string]string {
		t.Helper()
		env, err := prepareCodexWithModels(
			"https://api.everyapi.ai",
			"key",
			models,
			"gpt-5.6-sol",
		)
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	replaceIndex := func(env map[string]string, body string) {
		t.Helper()
		path := filepath.Join(env["CODEX_HOME"], "session_index.jsonl")
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}

	const namedEntry = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"safe","updated_at":"2026-08-09T00:00:00Z"}` + "\n"
	seed := prepare()
	replaceIndex(seed, namedEntry)
	TakePreparedCleanup(seed)()

	corrupt := prepare()
	corruptHome := corrupt["CODEX_HOME"]
	replaceIndex(corrupt, "{truncated\n")
	TakePreparedCleanup(corrupt)()
	if _, err := os.Stat(corruptHome); err != nil {
		t.Fatalf("corrupt launch home was not preserved: %v", err)
	}
	t.Cleanup(func() { removePreparedHomeAfterQuiet(corruptHome) })

	body, err := os.ReadFile(filepath.Join(persistentHome, "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != namedEntry {
		t.Fatalf("persistent session index = %q after corrupt update, want %q", body, namedEntry)
	}
}

func TestMergeCodexSessionIndexRejectsIncompleteEntry(t *testing.T) {
	const original = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"safe","updated_at":"2026-08-09T00:00:00Z"}` + "\n"

	if _, err := mergeCodexSessionIndex([]byte(original), []byte(`{"id":"11111111-1111-4111-8111-111111111111"}`+"\n"), []byte(original)); err == nil {
		t.Fatal("mergeCodexSessionIndex accepted an incomplete session-index entry")
	}
}

func TestPrepareCodexWithModelsDoesNotLetStaleDeleteRemoveConcurrentRename(t *testing.T) {
	codexTestHome(t)
	stubCodexBundledCatalog(t)
	models := []Model{{ID: "gpt-5.6-sol"}}
	prepare := func() map[string]string {
		t.Helper()
		env, err := prepareCodexWithModels("https://api.everyapi.ai", "key", models, "gpt-5.6-sol")
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	replaceIndex := func(env map[string]string, body string) {
		t.Helper()
		path := filepath.Join(env["CODEX_HOME"], "session_index.jsonl")
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}

	const original = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"old","updated_at":"2026-08-09T00:00:00Z"}` + "\n"
	const renamed = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"new","updated_at":"2026-08-09T00:00:02Z"}` + "\n"
	seed := prepare()
	replaceIndex(seed, original)
	TakePreparedCleanup(seed)()

	deleter := prepare()
	renamer := prepare()
	replaceIndex(deleter, "")
	replaceIndex(renamer, renamed)
	TakePreparedCleanup(renamer)()
	TakePreparedCleanup(deleter)()

	check := prepare()
	defer TakePreparedCleanup(check)()
	body, err := os.ReadFile(filepath.Join(check["CODEX_HOME"], "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	latest, err := latestCodexSessionIndexLines(body)
	if err != nil {
		t.Fatal(err)
	}
	if got := latest["11111111-1111-4111-8111-111111111111"].Entry.ThreadName; got != "new" {
		t.Fatalf("latest session name = %q after rename/delete race, want %q\n%s", got, "new", body)
	}
}

func TestMergeCodexSessionIndexRestoresConcurrentRenameAfterDelete(t *testing.T) {
	const original = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"old","updated_at":"2026-08-09T00:00:00Z"}` + "\n"
	const renamed = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"new","updated_at":"2026-08-09T00:00:02Z"}` + "\n"

	merged, err := mergeCodexSessionIndex([]byte(original), []byte(renamed), nil)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := latestCodexSessionIndexLines(merged)
	if err != nil {
		t.Fatal(err)
	}
	if got := latest["11111111-1111-4111-8111-111111111111"].Entry.ThreadName; got != "new" {
		t.Fatalf("latest session name = %q after delete/rename race, want %q\n%s", got, "new", merged)
	}
}

func TestPrepareCodexWithModelsKeepsNewestConcurrentRename(t *testing.T) {
	codexTestHome(t)
	stubCodexBundledCatalog(t)
	models := []Model{{ID: "gpt-5.6-sol"}}
	prepare := func() map[string]string {
		t.Helper()
		env, err := prepareCodexWithModels("https://api.everyapi.ai", "key", models, "gpt-5.6-sol")
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	replaceIndex := func(env map[string]string, body string) {
		t.Helper()
		path := filepath.Join(env["CODEX_HOME"], "session_index.jsonl")
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}

	const original = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"old","updated_at":"2026-08-09T00:00:00Z"}` + "\n"
	const olderRename = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"older","updated_at":"2026-08-09T00:00:01Z"}` + "\n"
	const newerRename = `{"id":"11111111-1111-4111-8111-111111111111","thread_name":"newer","updated_at":"2026-08-09T00:00:02Z"}` + "\n"
	seed := prepare()
	replaceIndex(seed, original)
	TakePreparedCleanup(seed)()

	older := prepare()
	newer := prepare()
	replaceIndex(older, olderRename)
	replaceIndex(newer, newerRename)
	TakePreparedCleanup(newer)()
	TakePreparedCleanup(older)()

	check := prepare()
	defer TakePreparedCleanup(check)()
	body, err := os.ReadFile(filepath.Join(check["CODEX_HOME"], "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	latest, err := latestCodexSessionIndexLines(body)
	if err != nil {
		t.Fatal(err)
	}
	if got := latest["11111111-1111-4111-8111-111111111111"].Entry.ThreadName; got != "newer" {
		t.Fatalf("latest session name = %q after concurrent renames, want %q\n%s", got, "newer", body)
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
		t.Fatalf("CODEX_SQLITE_HOME = %q, want launch-local SQLite state", got)
	}
	if info, err := os.Stat(filepath.Join(env["CODEX_HOME"], "sessions")); err != nil {
		t.Fatalf("stat linked sessions directory: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("linked sessions path is not a directory: %v", info.Mode())
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

// TestClaudeTool_NoPrepare guards the negative case: claude
// don't need pre-exec setup, so tool.Prepare must be a clean no-op
// (nil map, nil err). A future regression that accidentally wires a
// shared prepareFn would silently create unwanted ~/.config/everyapi
// dirs for those flows.
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

// stubCodexBundledCatalog replaces the bundled-metadata read, which otherwise
// shells out to the real `codex` binary. CI has no codex on PATH, so any test
// that passes a non-empty model list must stub this or it fails there while
// passing on a developer machine that happens to have the CLI installed.
func stubCodexBundledCatalog(t *testing.T) {
	t.Helper()
	original := codexBundledCatalog
	codexBundledCatalog = func() ([]byte, error) {
		return []byte(`{"models":[{"slug":"gpt-template","display_name":"Template","description":"template","default_reasoning_level":null,"supported_reasoning_levels":[],"shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,"availability_nux":null,"upgrade":null,"base_instructions":"You are a coding agent.","support_verbosity":false,"default_verbosity":null,"apply_patch_tool_type":"freeform","truncation_policy":{"mode":"bytes","limit":10000},"supports_parallel_tool_calls":true,"experimental_supported_tools":[]}]}`), nil
	}
	t.Cleanup(func() { codexBundledCatalog = original })
}

// TestPrepareCodex_SeedsBootModelIntoAFreshHome covers the gap the
// "root-level model is preserved" contract cannot fill on the live-catalog
// path. That path hands codex a process-scoped CODEX_HOME created by
// os.MkdirTemp and deleted on exit, so there is never a previous config.toml
// to preserve a model from — whatever codex recorded about its own model died
// with the last home. The catalogue's first entry is the selection EveryAPI
// persisted, so it seeds the boot model instead.
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
	t.Cleanup(func() { _ = os.RemoveAll(home) })

	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `model = "claude-opus-4-8"`) {
		t.Fatalf("fresh home did not get the catalogue's first model as its boot model:\n%s", body)
	}
}

// TestPrepareCodex_BootModelDoesNotOverrideAUserChoice keeps the seeding
// subordinate to the existing contract: a model the user set in a home that
// survives is still preserved, and the seed only fills an empty field.
func TestPrepareCodex_BootModelDoesNotOverrideAUserChoice(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"user-selected-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No models → legacy fixed home, which is the shape that can carry a
	// user's own config forward.
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

// TestPrepareCodexTransparent_SeedsBootModelIntoAFreshHome is the same seeding
// on the path codex actually takes by default. Transparent mode is the default
// for codex, so covering only the injected path would leave plain
// `everyapi use codex` still booting on whatever the catalogue listed first.
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
	t.Cleanup(func() { _ = os.RemoveAll(home) })

	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
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
	t.Cleanup(func() { _ = os.RemoveAll(env["CODEX_HOME"]) })

	body, err := os.ReadFile(filepath.Join(env["CODEX_HOME"], "config.toml"))
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
	t.Cleanup(func() { _ = os.RemoveAll(env["CODEX_HOME"]) })

	body, err := os.ReadFile(filepath.Join(env["CODEX_HOME"], "config.toml"))
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

// TestPrepareCodex_DoesNotPinAModelTheUserNeverChose is the guard against
// turning "EveryAPI has no preference" into "EveryAPI pinned position 0".
// Seeding the boot model from the catalogue's first entry rather than from an
// actual selection would silently override codex's own built-in default on
// every launch — a worse outcome than the alphabetical-ordering bug this
// branch set out to fix, and applied to users who never asked for a model.
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
			home := env["CODEX_HOME"]
			t.Cleanup(func() { _ = os.RemoveAll(home) })
			body, err := os.ReadFile(filepath.Join(home, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			// A root-level `model = ` line, not model_provider or
			// model_reasoning_effort or model_catalog_json.
			for _, line := range strings.Split(string(body), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "model = ") {
					t.Fatalf("codex was pinned to a model with no selection behind it: %q\nFull config:\n%s", line, body)
				}
			}
		})
	}
}
