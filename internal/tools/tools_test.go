package tools

import (
	"strings"
	"testing"
)

// TestRegistry_HasExpectedTools pins the V1 supported set. New tools
// can be added freely; removing one should be a deliberate spec
// change that breaks this test.
func TestRegistry_HasExpectedTools(t *testing.T) {
	want := []string{"claude", "codex", "gemini"}
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
	// No accidental OpenAI vars leaking through.
	if _, ok := env["OPENAI_API_KEY"]; ok {
		t.Error("claude env should not set OPENAI_API_KEY")
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

// TestEnv_Gemini verifies the Gemini env contract: /v1beta suffix
// matches Google's published path.
func TestEnv_Gemini(t *testing.T) {
	tool, _ := Lookup("gemini")
	env := tool.Env("https://api.everyapi.ai", "my-token")
	if got := env["GEMINI_API_KEY"]; got != "my-token" {
		t.Errorf("GEMINI_API_KEY = %q", got)
	}
	if got := env["GOOGLE_GEMINI_BASE_URL"]; got != "https://api.everyapi.ai/v1beta" {
		t.Errorf("GOOGLE_GEMINI_BASE_URL = %q (need /v1beta suffix)", got)
	}
}

// TestEnv_LocalDevBase verifies the joinBase helper doesn't insert
// double slashes when the user has a trailing-slash base (a
// surprisingly common dev typo: `EVERYAPI_BASE=http://localhost:8787/`).
// Gemini still proves this end-to-end via envFn; for codex the same
// joinBase invariant is covered through prepareCodex's config.toml
// (see TestPrepareCodex_TrailingSlashBase).
func TestEnv_LocalDevBase(t *testing.T) {
	tool, _ := Lookup("gemini")
	env := tool.Env("http://localhost:8787/", "tok")
	got := env["GOOGLE_GEMINI_BASE_URL"]
	if got != "http://localhost:8787/v1beta" {
		t.Errorf("GOOGLE_GEMINI_BASE_URL = %q (expected single slash join)", got)
	}
	// Defensive: no `://` anomalies introduced
	if strings.Contains(got, "//v1beta") {
		t.Errorf("found double slash: %q", got)
	}
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
