// Package sellertype is the single source of truth for the
// human-name ↔ backend-integer mapping of seller-mountable channel
// types. Shared by `everyapi seller …` (cmd/seller) and the MCP
// seller tools (internal/mcp) so the two surfaces can't drift.
//
// Error formatting deliberately stays with the callers: the CLI
// localizes via i18n, the MCP tools speak English only.
package sellertype

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Aliases maps human names to the server's integer channel type id.
// Curated to match the server-side allow-list for seller-mounted
// channels — anything not in that allowed set would 422 at submit,
// so listing it here would be misleading. Keep the keys lowercase;
// lookup folds case.
var Aliases = map[string]int{
	"openai":    1,
	"anthropic": 6,
	"claude":    6,
	"gemini":    13,
	"aws":       18,
	"bedrock":   18,
	"vertex":    26,
	"vertexai":  26,
	"deepseek":  28,
	"xai":       33,
	"grok":      33,
	"codex":     42,
}

// Resolve accepts either an alias name (openai / claude / …) or a
// numeric id straight through. Numeric passthrough is the escape
// hatch for any allowed type the alias map doesn't list yet, without
// blocking the user on a CLI release. Reports ok=false when the
// input is neither — callers format their own error message.
func Resolve(s string) (id int, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if id, ok := Aliases[s]; ok {
		return id, true
	}
	if id, err := strconv.Atoi(s); err == nil && id > 0 {
		return id, true
	}
	return 0, false
}

// Choices returns alias names in stable display order. We dedupe
// across the alias map (claude/anthropic both → 6) and prefer the
// marketing-recognisable spelling: "claude" not "anthropic",
// "vertex" not "vertexai".
func Choices() []string {
	preferred := []string{"openai", "claude", "gemini", "codex", "vertex", "aws", "xai", "deepseek"}
	// Sanity check that every preferred name resolves — guards against
	// silent drift if the alias map is renamed without updating this list.
	out := make([]string, 0, len(preferred))
	for _, n := range preferred {
		if _, ok := Aliases[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// Label returns the human alias for a backend integer id, for output
// rendering. Falls back to the raw id string when the integer is
// outside our alias map (forward-compat with future types).
func Label(id int) string {
	type kv struct {
		name string
		id   int
	}
	pairs := make([]kv, 0, len(Aliases))
	for n, i := range Aliases {
		pairs = append(pairs, kv{n, i})
	}
	// Stable iteration so the label for id=6 is deterministically
	// "claude" (the marketing name we prefer over "anthropic"), even
	// though both alias to 6. Sort puts the alphabetically-first first;
	// for the four ambiguous pairs ((anthropic, claude), (aws,
	// bedrock), (vertex, vertexai), (xai, grok)) we want claude / aws /
	// vertex / grok respectively. Hardcode the preferred display name
	// instead of relying on alphabetic luck.
	preferredFor := map[int]string{
		6:  "claude",
		18: "aws",
		26: "vertex",
		33: "xai",
	}
	if name, ok := preferredFor[id]; ok {
		return name
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].name < pairs[j].name })
	for _, p := range pairs {
		if p.id == id {
			return p.name
		}
	}
	return fmt.Sprintf("type=%d", id)
}
