package sellertype

import "testing"

// TestResolveKindSlug pins the alias→kind_slug contract to the backend
// allow-list (backend/internal/controller/channel_seller.go
// sellerAllowedChannelKinds). The backend binds `kind_slug` and 422s on
// anything outside this set, so a drift here silently breaks every
// `seller add-key` mount.
func TestResolveKindSlug(t *testing.T) {
	cases := []struct {
		in       string
		wantSlug string
		wantOK   bool
	}{
		{"openai", "openai", true},
		{"claude", "anthropic", true},
		{"anthropic", "anthropic", true},
		{"CLAUDE", "anthropic", true}, // case-insensitive
		{"  claude  ", "anthropic", true},
		{"gemini", "gemini", true},
		{"codex", "codex", true},
		{"vertex", "vertex_ai", true},
		{"vertexai", "vertex_ai", true},
		{"vertex_ai", "vertex_ai", true},
		{"aws", "aws", true},
		{"bedrock", "aws", true},
		{"xai", "xai", true},
		{"grok", "xai", true},
		{"deepseek", "deepseek", true},
		{"6", "", false}, // the retired integer contract must not resolve
		{"unknown", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		slug, ok := Resolve(c.in)
		if ok != c.wantOK || slug != c.wantSlug {
			t.Errorf("Resolve(%q) = (%q, %v), want (%q, %v)", c.in, slug, ok, c.wantSlug, c.wantOK)
		}
	}
}

// TestResolvedSlugsMatchBackendAllowList guards that every advertised
// choice resolves to one of the backend's accepted kind_slugs.
func TestResolvedSlugsMatchBackendAllowList(t *testing.T) {
	allowed := map[string]bool{
		"openai": true, "anthropic": true, "codex": true, "gemini": true,
		"vertex_ai": true, "aws": true, "xai": true, "deepseek": true,
	}
	for _, choice := range Choices() {
		slug, ok := Resolve(choice)
		if !ok {
			t.Errorf("advertised choice %q does not resolve", choice)
			continue
		}
		if !allowed[slug] {
			t.Errorf("choice %q resolves to %q which is not in the backend allow-list", choice, slug)
		}
	}
}

// TestLabelPrefersMarketingName mirrors the display contract: a slug
// with multiple aliases renders the marketing-recognisable spelling.
func TestLabelPrefersMarketingName(t *testing.T) {
	cases := map[string]string{
		"anthropic": "claude",
		"aws":       "aws",
		"vertex_ai": "vertex",
		"xai":       "xai",
		"openai":    "openai",
		"gemini":    "gemini",
		"codex":     "codex",
		"deepseek":  "deepseek",
		"mystery":   "mystery", // unknown slug → raw passthrough
		"":          "",
	}
	for slug, want := range cases {
		if got := Label(slug); got != want {
			t.Errorf("Label(%q) = %q, want %q", slug, got, want)
		}
	}
}
