package tools

import "testing"

// TestTransparentClaudePromptCaching1hMatchesInjectedPath pins the transparent
// overlay to the same 1h prompt-cache decision the injected (non-transparent)
// path makes for standalone `everyapi use claude`.
//
// Without this, a refactor of the shared transparentClaudeEnv could silently
// re-introduce the regression (transparent standalone claude losing the flag).
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
}
