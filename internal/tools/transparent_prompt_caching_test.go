package tools

import "testing"

// TestTransparentClaudePromptCaching1hMatchesInjectedPath pins the transparent
// overlay to the same 1h prompt-cache decision the injected (non-transparent)
// path makes, per tool:
//
//   - standalone `everyapi use claude` sets ENABLE_PROMPT_CACHING_1H in both
//     its injected env and (via transparentStandaloneClaudeEnv) its transparent
//     env, so a user who adds --transparent keeps the 1h window.
//   - the Claude Code provider presets (glm/kimi/…) set it in neither path, so
//     both stay byte-for-byte equivalent.
//
// Without this, a refactor of the shared transparentClaudeEnv could silently
// re-introduce the regression (transparent standalone claude losing the flag)
// or leak the flag into presets.
func TestTransparentClaudePromptCaching1hMatchesInjectedPath(t *testing.T) {
	const caPath = "/tmp/ca.pem"
	const flag = "ENABLE_PROMPT_CACHING_1H"

	claude, err := Lookup("claude")
	if err != nil {
		t.Fatalf("Lookup(claude): %v", err)
	}
	if claude.Env("https://api.everyapi.ai", "tok")[flag] != "1" {
		t.Fatal("standalone claude injected env must set ENABLE_PROMPT_CACHING_1H=1")
	}
	set, _, err := claude.TransparentEnv("http://127.0.0.1:9999", caPath)
	if err != nil {
		t.Fatalf("claude.TransparentEnv: %v", err)
	}
	if set[flag] != "1" {
		t.Fatalf("standalone claude transparent env must set %s=1 (got %q)", flag, set[flag])
	}

	for _, name := range []string{"glm", "kimi", "minimax", "qwen", "deepseek", "byteplus"} {
		preset, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%s): %v", name, err)
		}
		if _, ok := preset.Env("https://api.everyapi.ai", "tok")[flag]; ok {
			t.Errorf("preset %s injected env must NOT set %s (stay equivalent to injected path)", name, flag)
		}
		presetSet, _, err := preset.TransparentEnv("http://127.0.0.1:9999", caPath)
		if err != nil {
			t.Fatalf("%s.TransparentEnv: %v", name, err)
		}
		if _, ok := presetSet[flag]; ok {
			t.Errorf("preset %s transparent env must NOT set %s (stay equivalent to injected path)", name, flag)
		}
	}
}
