package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestRegistry_HasExpectedTools pins the V1 supported set. New tools
// can be added freely; removing one should be a deliberate spec
// change that breaks this test.
func TestRegistry_HasExpectedTools(t *testing.T) {
	want := []string{
		"claude", "codex", "gemini", "grok", "qwen-code", "kimi-code", "hermes",
		"minimax", "qwen", "deepseek", "byteplus", "glm", "kimi",
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

// TestEnv_Claude verifies the Anthropic env contract: no /v1 suffix
// (their SDK appends its own version path), token in AUTH_TOKEN not
// API_KEY (Claude Code's documented variable).
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
	// ANTHROPIC_API_KEY must be present and empty: mergeEnv overlays it
	// onto the child env to neutralise any ambient real Anthropic key so
	// it can't leak to the gateway or shadow ANTHROPIC_AUTH_TOKEN.
	if got, ok := env["ANTHROPIC_API_KEY"]; !ok || got != "" {
		t.Errorf("ANTHROPIC_API_KEY = %q (present=%v), want present and empty", got, ok)
	}
	// No accidental OpenAI vars leaking through.
	if _, ok := env["OPENAI_API_KEY"]; ok {
		t.Error("claude env should not set OPENAI_API_KEY")
	}
}

// TestClaudeProviderPreset verifies the third-party provider presets:
// they launch the `claude` binary (Claude Code) and carry a ModelOwner
// (the /v1/models `owned_by` cmd/use filters the picker on), but the
// model itself is NOT baked into envFn — it's chosen at launch and
// injected later. envFn must still honor claude's Anthropic env
// contract (raw base URL, AUTH_TOKEN, no OpenAI vars, no premature
// ANTHROPIC_MODEL).
func TestClaudeProviderPreset(t *testing.T) {
	// Owners are the model BRAND the gateway reports in owned_by (derived
	// from the model id), not the channel-adaptor name. qwen/glm moved off
	// their old channel names ("ali"/"zhipu_4v") when owned_by was
	// de-channelized; cmd/use's legacyOwnerAliases still tolerates the old
	// values during rollout.
	wantOwner := map[string]string{
		"minimax":  "minimax",
		"qwen":     "qwen",
		"deepseek": "deepseek",
		"byteplus": "byteplus",
		"glm":      "zhipu",
		"kimi":     "moonshot",
	}
	for name, owner := range wantOwner {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		if tool.ExecName != "claude" {
			t.Errorf("%s ExecName = %q, want claude", name, tool.ExecName)
		}
		if tool.ModelOwner != owner {
			t.Errorf("%s ModelOwner = %q, want %q", name, tool.ModelOwner, owner)
		}
		env := tool.Env("https://api.everyapi.ai", "my-token")
		if got := env["ANTHROPIC_BASE_URL"]; got != "https://api.everyapi.ai" {
			t.Errorf("%s ANTHROPIC_BASE_URL = %q", name, got)
		}
		if got := env["ANTHROPIC_AUTH_TOKEN"]; got != "my-token" {
			t.Errorf("%s ANTHROPIC_AUTH_TOKEN = %q", name, got)
		}
		if got := env["ENABLE_TOOL_SEARCH"]; got != "1" {
			t.Errorf("%s ENABLE_TOOL_SEARCH = %q, want 1", name, got)
		}
		if _, ok := env["ENABLE_PROMPT_CACHING_1H"]; ok {
			t.Errorf("%s must not force Anthropic's 1h cache TTL on a third-party provider", name)
		}
		if got, ok := env["ANTHROPIC_API_KEY"]; !ok || got != "" {
			t.Errorf("%s ANTHROPIC_API_KEY = %q (present=%v), want present and empty", name, got, ok)
		}
		if _, ok := env["ANTHROPIC_MODEL"]; ok {
			t.Errorf("%s envFn should not pin ANTHROPIC_MODEL (chosen at launch)", name)
		}
		if _, ok := env["OPENAI_API_KEY"]; ok {
			t.Errorf("%s env should not set OPENAI_API_KEY", name)
		}
	}
}

// TestSetClaudeModel verifies the launch-time model injection points the
// main model AND the background/"haiku" model (current + deprecated var)
// at the same id — so Claude Code never falls back to a default haiku
// the gateway can't route.
func TestSetClaudeModel(t *testing.T) {
	env := map[string]string{"ANTHROPIC_BASE_URL": "https://api.everyapi.ai"}
	SetClaudeModel(env, "glm-5.1")
	for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		if env[k] != "glm-5.1" {
			t.Errorf("%s = %q, want glm-5.1", k, env[k])
		}
	}
}

// TestEnv_Codex verifies codex's env contract: only OPENAI_API_KEY.
// Codex does NOT honor OPENAI_BASE_URL at runtime — its router is
// pinned to ~/.codex/config.toml's model_provider. The base_url is
// injected via the prepareFn (CODEX_HOME → config.toml) instead.
func TestEnv_Codex(t *testing.T) {
	tool, _ := Lookup("codex")
	env := tool.Env("https://api.everyapi.ai", "my-token")
	if got := env["OPENAI_API_KEY"]; got != "my-token" {
		t.Errorf("OPENAI_API_KEY = %q", got)
	}
	if _, ok := env["OPENAI_BASE_URL"]; ok {
		// If you're re-adding OPENAI_BASE_URL, double-check codex
		// actually reads it — last time we checked, it doesn't,
		// and setting it created confusing "the env is set but
		// requests still hit api.openai.com" debug sessions.
		t.Error("codex env should not set OPENAI_BASE_URL (codex routes via config.toml model_provider, see prepareFn)")
	}
}

// TestEnv_Gemini verifies that the native Antigravity child never receives
// EveryAPI's relay credential or gateway routing variables.
func TestEnv_Gemini(t *testing.T) {
	tool, _ := Lookup("gemini")
	env := tool.Env("https://api.everyapi.ai", "my-token")
	if len(env) != 0 {
		t.Errorf("native agy Env should be empty, got %v", env)
	}
}

// TestEnv_Grok verifies Grok Build's documented environment contract and
// keeps its browser-login state separate from EveryAPI's relay credential.
func TestEnv_Grok(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tool, err := Lookup("grok")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "grok" {
		t.Errorf("ExecName = %q, want grok", tool.ExecName)
	}
	if tool.InstallCmd != "npm install -g @xai-official/grok" {
		t.Errorf("InstallCmd = %q", tool.InstallCmd)
	}
	if tool.YoloFlag != "--always-approve" {
		t.Errorf("YoloFlag = %q, want --always-approve", tool.YoloFlag)
	}
	if tool.RequiredEndpoint != "openai-response" {
		t.Errorf("RequiredEndpoint = %q, want openai-response", tool.RequiredEndpoint)
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

	extra, err := tool.Prepare("https://api.everyapi.ai", "my-token")
	if err != nil {
		t.Fatal(err)
	}
	wantHome := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "everyapi", "grok-home")
	if got := extra["GROK_HOME"]; got != wantHome {
		t.Errorf("GROK_HOME = %q, want %q", got, wantHome)
	}
	if info, err := os.Stat(wantHome); err != nil {
		t.Fatalf("stat GROK_HOME: %v", err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("GROK_HOME permissions = %o, want 0700", info.Mode().Perm())
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
	if tool.InstallCmd != "npm install -g @qwen-code/qwen-code@latest" {
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
	if tool.InstallCmd != "npm install -g @moonshot-ai/kimi-code" {
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

// TestEnv_Hermes verifies hermes' env contract: envFn sets NOTHING.
// All routing (base_url + relay key) goes through the generated
// config.yaml in prepareHermes; in particular OPENAI_BASE_URL /
// OPENAI_API_KEY must NOT be set, since hermes ignores OPENAI_BASE_URL
// for its main model and only honors OPENAI_API_KEY for openai.com
// hosts — setting them would create confusing "env is set but nothing
// routes" debug sessions. HERMES_HOME comes from prepareFn, not here.
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
				"ANTHROPIC_AUTH_TOKEN": "everyapi-local-connector",
				"NODE_EXTRA_CA_CERTS":  caPath,
			},
			wantUnset: []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"},
		},
		{
			name: "codex",
			want: map[string]string{
				"OPENAI_API_KEY":       "everyapi-local-connector",
				"CODEX_CA_CERTIFICATE": caPath,
			},
			wantUnset: []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"},
		},
		{
			name: "glm",
			want: map[string]string{
				"ANTHROPIC_AUTH_TOKEN": "everyapi-local-connector",
				"NODE_EXTRA_CA_CERTS":  caPath,
			},
			wantUnset: []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"},
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
	for _, name := range []string{"claude", "codex", "glm", "kimi"} {
		tool, _ := Lookup(name)
		if !tool.SupportsTransparent() {
			t.Errorf("%s should support transparent mode", name)
		}
	}
	for _, name := range []string{"gemini", "grok", "qwen-code", "kimi-code", "hermes"} {
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

// TestLookup_Unknown returns an error listing supported names so the
// CLI doesn't have to maintain a parallel list.
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

// TestLookup_CaseInsensitive — users typing `everyapi use Claude`
// shouldn't get a "not found" surprise.
func TestLookup_CaseInsensitive(t *testing.T) {
	if _, err := Lookup("Claude"); err != nil {
		t.Errorf("Lookup(\"Claude\") error: %v", err)
	}
}

// TestMergeEnv asserts the env-overlay semantics relied on by
// syscall.Exec: keys in `set` override matching entries from the
// parent env; non-matching parent entries pass through; keys in
// `set` not present in the parent are appended.
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
	// Sanity: no duplicate keys (a buggy overlay would emit both the
	// parent and the override entries).
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
