package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hermesTestHome redirects ConfigDir() at a fresh tmp dir for one test by hijacking XDG_CONFIG_HOME (which the SDK's ConfigDir honors first). Returns the resolved HERMES_HOME prepareHermes should produce so the test can assert paths without re-computing the join.
func hermesTestHome(t *testing.T) (xdg, wantHermesHome string) {
	t.Helper()
	xdg = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv(hermesModelEnv, "catalog-model")
	return xdg, filepath.Join(xdg, "everyapi", "hermes-home")
}

// TestPrepareHermes_WritesConfig is the smoke test: if it breaks, `everyapi use hermes` won't route through the gateway. Verifies the custom-provider config.yaml lands in HERMES_HOME with base_url, the inline relay key, and the default model.
func TestPrepareHermes_WritesConfig(t *testing.T) {
	_, hermesHome := hermesTestHome(t)

	extra, err := prepareHermes("https://api.everyapi.ai", "sk-everyapi-abc")
	if err != nil {
		t.Fatalf("prepareHermes error: %v", err)
	}
	if got := extra["HERMES_HOME"]; got != hermesHome {
		t.Errorf("HERMES_HOME env = %q, want %q", got, hermesHome)
	}

	cfgBody, err := os.ReadFile(filepath.Join(hermesHome, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	cfg := string(cfgBody)
	for _, want := range []string{
		"provider: custom",
		`default: "catalog-model"`,
		`base_url: "https://api.everyapi.ai/v1"`,
		`api_key: "sk-everyapi-abc"`,
		"api_mode: chat_completions",
		"custom_providers:",
		`  - name: "EveryAPI"`,
		"    discover_models: true",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.yaml missing %q\nFull config:\n%s", want, cfg)
		}
	}
}

func TestPrepareHermesRequiresResolvedModel(t *testing.T) {
	hermesTestHome(t)
	t.Setenv(hermesModelEnv, "")

	if _, err := prepareHermes("https://api.everyapi.ai", "tok"); err == nil {
		t.Fatal("prepareHermes should reject an empty model instead of inserting a hardcoded fallback")
	}
}

// TestPrepareHermes_ModelOverride verifies EVERYAPI_HERMES_MODEL changes the boot model without touching anything else.
func TestPrepareHermes_ModelOverride(t *testing.T) {
	_, hermesHome := hermesTestHome(t)
	t.Setenv(hermesModelEnv, "gpt-5.1")

	if _, err := prepareHermes("https://api.everyapi.ai", "tok"); err != nil {
		t.Fatalf("prepareHermes error: %v", err)
	}
	cfgBody, err := os.ReadFile(filepath.Join(hermesHome, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	cfg := string(cfgBody)
	if !strings.Contains(cfg, `default: "gpt-5.1"`) {
		t.Errorf("EVERYAPI_HERMES_MODEL override not applied\nFull config:\n%s", cfg)
	}
	if strings.Contains(cfg, `default: "catalog-model"`) {
		t.Errorf("test catalog model leaked despite override\nFull config:\n%s", cfg)
	}
}

// TestPrepareHermes_ModelEscaped guards against YAML injection through EVERYAPI_HERMES_MODEL (user-supplied env): a model containing a quote or newline must stay confined to the default: scalar and must not inject a sibling key into the model map.
func TestPrepareHermes_ModelEscaped(t *testing.T) {
	_, hermesHome := hermesTestHome(t)
	// A value that, unescaped, would close the quote and add a key.
	t.Setenv(hermesModelEnv, "evil\"\napi_mode: completions")

	if _, err := prepareHermes("https://api.everyapi.ai", "tok"); err != nil {
		t.Fatalf("prepareHermes error: %v", err)
	}
	cfgBody, err := os.ReadFile(filepath.Join(hermesHome, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	cfg := string(cfgBody)
	// The quote and newline must be backslash-escaped, keeping the whole payload confined to the default: scalar (one physical line).
	if !strings.Contains(cfg, `default: "evil\"\napi_mode: completions"`) {
		t.Errorf("model not YAML-escaped\nFull config:\n%s", cfg)
	}
	// Structurally: the injected directive must not surface as its own line, and api_mode must appear exactly twice: once for the active model and once for the named custom provider used by Hermes' native picker.
	var apiModeLines int
	for _, line := range strings.Split(cfg, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "api_mode: completions" {
			t.Errorf("YAML injection succeeded: stray api_mode line\nFull config:\n%s", cfg)
		}
		if strings.HasPrefix(trimmed, "api_mode:") {
			apiModeLines++
		}
	}
	if apiModeLines != 2 {
		t.Errorf("expected exactly two legitimate api_mode lines, got %d\nFull config:\n%s", apiModeLines, cfg)
	}
}

// TestPrepareHermes_TrailingSlashBase pins the joinBase invariant for hermes' config.yaml: a dev-style `http://localhost:8787/` must not produce `http://localhost:8787//v1`.
func TestPrepareHermes_TrailingSlashBase(t *testing.T) {
	_, hermesHome := hermesTestHome(t)
	if _, err := prepareHermes("http://localhost:8787/", "tok"); err != nil {
		t.Fatalf("prepareHermes error: %v", err)
	}
	cfgBody, err := os.ReadFile(filepath.Join(hermesHome, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	cfg := string(cfgBody)
	if !strings.Contains(cfg, `base_url: "http://localhost:8787/v1"`) {
		t.Errorf("expected single-slash join in base_url, got config:\n%s", cfg)
	}
	if strings.Contains(cfg, "//v1") {
		t.Error("found double slash in base_url")
	}
}

// TestPrepareHermes_FilePerms guards the secret-bearing config.yaml: it carries an inline relay key, so chmod 0600 (and 0700 on the directory) is non-negotiable. Skipped on Windows where Unix perms don't apply.
func TestPrepareHermes_FilePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits not meaningful on Windows")
	}
	_, hermesHome := hermesTestHome(t)
	if _, err := prepareHermes("https://api.everyapi.ai", "tok"); err != nil {
		t.Fatalf("prepareHermes error: %v", err)
	}
	info, err := os.Stat(filepath.Join(hermesHome, "config.yaml"))
	if err != nil {
		t.Fatalf("stat config.yaml: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.yaml perm = %o, want 0600 (carries inline relay key)", perm)
	}
	dirInfo, err := os.Stat(hermesHome)
	if err != nil {
		t.Fatalf("stat hermes-home: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("hermes-home perm = %o, want 0700", perm)
	}
}

// TestPrepareHermes_Idempotent re-runs on the same directory and verifies the new relay key wins on the second call (covers the "user rotated keys / switched groups" case). The directory is reused on purpose, so the rewrite-every-call contract matters.
func TestPrepareHermes_Idempotent(t *testing.T) {
	_, hermesHome := hermesTestHome(t)
	if _, err := prepareHermes("https://api.everyapi.ai", "first-key"); err != nil {
		t.Fatalf("first prepareHermes: %v", err)
	}
	if _, err := prepareHermes("https://api.everyapi.ai", "second-key"); err != nil {
		t.Fatalf("second prepareHermes: %v", err)
	}
	cfgBody, err := os.ReadFile(filepath.Join(hermesHome, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	cfg := string(cfgBody)
	if !strings.Contains(cfg, `api_key: "second-key"`) {
		t.Errorf("api_key after rerun didn't update to second-key\nFull config:\n%s", cfg)
	}
	if strings.Contains(cfg, `api_key: "first-key"`) {
		t.Errorf("stale first-key still present after rerun\nFull config:\n%s", cfg)
	}
}

// TestHermesTool_PrepareWired makes sure the Registry entry actually invokes prepareHermes — a refactor that drops prepareFn from the hermes entry would leave the function existing-but-unused and the CLI silently regressing (no config.yaml → first-run wizard / 401).
func TestHermesTool_PrepareWired(t *testing.T) {
	_, hermesHome := hermesTestHome(t)
	tool, _ := Lookup("hermes")
	extra, err := tool.Prepare("https://api.everyapi.ai", "tok")
	if err != nil {
		t.Fatalf("tool.Prepare error: %v", err)
	}
	if extra["HERMES_HOME"] != hermesHome {
		t.Errorf("hermes tool.Prepare didn't run prepareHermes (HERMES_HOME=%q)", extra["HERMES_HOME"])
	}
}

// TestHermesTool_ModelEnvWired pins that the Registry exposes the model env var the picker drives — if this drops, `everyapi use hermes` stops offering the model picker and silently snaps to the default.
func TestHermesTool_ModelEnvWired(t *testing.T) {
	tool, _ := Lookup("hermes")
	if tool.ModelEnv != hermesModelEnv {
		t.Errorf("hermes ModelEnv = %q, want %q", tool.ModelEnv, hermesModelEnv)
	}
}

// TestLastHermesModel round-trips through the writer: the model pinned in config.yaml is exactly what LastHermesModel reads back, so the picker can default the cursor to the previous choice.
func TestLastHermesModel(t *testing.T) {
	hermesTestHome(t)

	// No config yet → empty.
	if got := LastHermesModel(); got != "" {
		t.Errorf("LastHermesModel() with no config = %q, want empty", got)
	}

	t.Setenv(hermesModelEnv, "gpt-5.1")
	if _, err := prepareHermes("https://api.everyapi.ai", "tok"); err != nil {
		t.Fatalf("prepareHermes error: %v", err)
	}
	if got := LastHermesModel(); got != "gpt-5.1" {
		t.Errorf("LastHermesModel() = %q, want %q", got, "gpt-5.1")
	}
}
