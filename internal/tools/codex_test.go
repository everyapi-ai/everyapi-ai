package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

// TestClaudeTool_NoPrepare guards the negative case: claude/gemini
// don't need pre-exec setup, so tool.Prepare must be a clean no-op
// (nil map, nil err). A future regression that accidentally wires a
// shared prepareFn would silently create unwanted ~/.config/everyapi
// dirs for those flows.
func TestClaudeTool_NoPrepare(t *testing.T) {
	for _, name := range []string{"claude", "gemini"} {
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
