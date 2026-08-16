// Package sellertype is the single source of truth for the human-name ↔ backend kind_slug mapping of seller-mountable channel types. Shared by `everyapi seller …` (cmd/seller) and the MCP seller tools (internal/mcp) so the two surfaces can't drift.
//
// The backend retired the numeric `type` column for seller mounts: channeladmin.SellerChannelCreate now binds `kind_slug string` and validates it against a fixed allow-list (openai / anthropic / codex / gemini / vertex_ai / aws / xai / deepseek — see backend/internal/transport/http/channeladmin/seller.go sellerAllowedChannelKinds). This package therefore maps human aliases to those canonical slugs; sending the old integer id makes the backend drop the field and 422.
//
// Error formatting deliberately stays with the callers: the CLI localizes via i18n, the MCP tools speak English only.
package sellertype

import (
	"sort"
	"strings"
)

// Aliases maps accepted human inputs to the backend's canonical kind_slug. Both the marketing spellings (claude, vertex, bedrock, grok) and the canonical slugs themselves are accepted as input so a user can type either; the value is always the exact slug the backend allow-list expects. Keys are lowercase; lookup folds case.
//
// Curated to match the server-side allow-list for seller-mounted channels — anything not in that allowed set would 422 at submit, so listing it here would be misleading.
var Aliases = map[string]string{
	"openai":    "openai",
	"anthropic": "anthropic",
	"claude":    "anthropic",
	"gemini":    "gemini",
	"codex":     "codex",
	"vertex_ai": "vertex_ai",
	"vertex":    "vertex_ai",
	"vertexai":  "vertex_ai",
	"aws":       "aws",
	"bedrock":   "aws",
	"xai":       "xai",
	"grok":      "xai",
	"deepseek":  "deepseek",
}

// Resolve accepts an alias name (openai / claude / vertex / …) or a canonical slug (anthropic / vertex_ai / …) and returns the backend kind_slug. Reports ok=false when the input is neither — callers format their own error message listing Choices(). Unlike the old integer contract there is no numeric passthrough: the slug allow-list is fixed and small, so an unknown input is a typo we should catch locally with a helpful list rather than forward to a backend 422.
func Resolve(s string) (slug string, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if slug, ok := Aliases[s]; ok {
		return slug, true
	}
	return "", false
}

// Choices returns alias names in stable display order. We dedupe across the alias map (claude/anthropic both → anthropic) and prefer the marketing-recognisable spelling: "claude" not "anthropic", "vertex" not "vertexai".
func Choices() []string {
	preferred := []string{"openai", "claude", "gemini", "codex", "vertex", "aws", "xai", "deepseek"}
	// Sanity check that every preferred name resolves — guards against silent drift if the alias map is renamed without updating this list.
	out := make([]string, 0, len(preferred))
	for _, n := range preferred {
		if _, ok := Aliases[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// Label returns the human alias for a backend kind_slug, for output rendering. Falls back to the raw slug when it is outside our alias map (forward-compat with future kinds).
func Label(slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return ""
	}
	// For slugs with multiple aliases, hardcode the preferred display name instead of relying on alphabetic luck: anthropic→claude, aws (not bedrock), vertex_ai→vertex, xai (not grok).
	preferredFor := map[string]string{
		"anthropic": "claude",
		"aws":       "aws",
		"vertex_ai": "vertex",
		"xai":       "xai",
	}
	if name, ok := preferredFor[slug]; ok {
		return name
	}
	// Otherwise return the alias whose canonical slug matches, in a deterministic order so the label is stable across map iteration.
	names := make([]string, 0, len(Aliases))
	for n, s := range Aliases {
		if s == slug {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		sort.Strings(names)
		return names[0]
	}
	return slug
}
