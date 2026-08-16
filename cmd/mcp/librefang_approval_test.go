package mcp

import (
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/mcp"
)

// LibreFang namespaces every MCP tool as `mcp_<server>_<tool>` and does NOT de-duplicate, so our already-`everyapi_`-prefixed names come out doubly prefixed. This mirrors `format_mcp_tool_name` in librefang-runtime-mcp/src/lib.rs (`mcp_{normalize(server)}_{normalize(tool)}`, where normalize lowercases and maps `-` to `_`). The install snippet pins the server name to "everyapi", so that half is constant here.
func namespaced(tool string) string {
	return "mcp_everyapi_" + strings.ToLower(strings.ReplaceAll(tool, "-", "_"))
}

// libreFangGlobMatches implements the subset of LibreFang's `glob_matches` (librefang-types/src/capability.rs) that our patterns actually use: exact match, or a single trailing `*` prefix match. LibreFang's matcher has more cases (leading `*`, path- and host-segment awareness), but a pattern reaching them would be rejected earlier by `validate_tool_name`, which permits only alphanumerics, `_`, and at most one `*`.
func libreFangGlobMatches(pattern, value string) bool {
	if pattern == value {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok && !strings.Contains(prefix, "*") {
		return strings.HasPrefix(value, prefix)
	}
	return false
}

// writeTools are the 8 tools that change state: they move money, upload a plaintext upstream credential, rewrite deployment-wide settings, destroy an edge registration, or mount a channel via OAuth.
var writeTools = []string{
	"everyapi_seller_withdraw",
	"everyapi_seller_add_key",
	"everyapi_admin_marketplace_set",
	"everyapi_edge_remove",
	"everyapi_seller_add_oauth_codex_start",
	"everyapi_seller_add_oauth_codex_poll",
	"everyapi_seller_add_oauth_claude_start",
	"everyapi_seller_add_oauth_claude_complete",
}

// readTools are the 7 that only report. `everyapi_topup` is a read despite the name: it returns the wallet URL as a string and moves no money.
var readTools = []string{
	"everyapi_status",
	"everyapi_topup",
	"everyapi_seller_list",
	"everyapi_seller_eligibility",
	"everyapi_edge_list",
	"everyapi_edge_status",
	"everyapi_admin_marketplace_status",
}

// libreFangApprovalGlobs must stay byte-identical to the patterns published on https://librefang.ai/integrations/mcp-a2a and pinned there by crates/librefang-extensions/tests/everyapi_catalog_entry.rs. Two independent docs telling a user two different approval configs is the failure this guards: a user who follows one and then the other ends up with a config that contradicts itself.
var libreFangApprovalGlobs = []string{
	"mcp_everyapi_everyapi_seller_add_*",
	"mcp_everyapi_everyapi_seller_withdraw",
	"mcp_everyapi_everyapi_admin_marketplace_set",
	"mcp_everyapi_everyapi_edge_remove",
}

// TestLibreFangApprovalGlobsGateWritesAndOnlyWrites is the substantive check on the advice we print. Gating too little leaves a money-moving tool open; gating too much (a blanket `mcp_everyapi_*`) stalls `everyapi_status` on an approval prompt, which breaks the unattended "check the balance before an expensive job" use case LibreFang's own page recommends.
func TestLibreFangApprovalGlobsGateWritesAndOnlyWrites(t *testing.T) {
	gated := func(tool string) bool {
		for _, p := range libreFangApprovalGlobs {
			if libreFangGlobMatches(p, namespaced(tool)) {
				return true
			}
		}
		return false
	}

	for _, tool := range writeTools {
		if !gated(tool) {
			t.Errorf("write tool %s is not covered by the printed require_approval globs", tool)
		}
	}
	for _, tool := range readTools {
		if gated(tool) {
			t.Errorf("read tool %s is gated by the printed globs — a balance check would stall on approval", tool)
		}
	}

	// A blanket pattern is the tempting shorthand and the reason this test exists; prove it would break the read path.
	if !libreFangGlobMatches("mcp_everyapi_*", namespaced("everyapi_status")) {
		t.Fatal("test's own glob semantics are wrong: mcp_everyapi_* must match a read tool")
	}
}

// TestLibreFangPostInstallNotePrintsTheseGlobs ties the tested pattern set to the text a user actually sees. Without it the constants above could stay correct while the printed note drifted.
func TestLibreFangPostInstallNotePrintsTheseGlobs(t *testing.T) {
	c, err := lookupClient("librefang")
	if err != nil {
		t.Fatal(err)
	}
	got := manualInstallText(c)
	for _, p := range libreFangApprovalGlobs {
		if !strings.Contains(got, p) {
			t.Errorf("install text omits approval glob %q:\n%s", p, got)
		}
	}
	// The blanket glob legitimately appears in prose as the thing NOT to do, so a bare substring check would misfire. Scan only lines that are entirely a quoted pattern — the paste-ready shape — and require each to be vetted.
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimRight(strings.TrimSpace(line), ",")
		if len(trimmed) < 2 || !strings.HasPrefix(trimmed, `"`) || !strings.HasSuffix(trimmed, `"`) {
			continue
		}
		pattern := trimmed[1 : len(trimmed)-1]
		if strings.ContainsAny(pattern, `" `) {
			continue
		}
		known := false
		for _, p := range libreFangApprovalGlobs {
			if p == pattern {
				known = true
				break
			}
		}
		if !known {
			t.Errorf("install text offers an unvetted approval glob %q as paste-ready — every recommended pattern must be in libreFangApprovalGlobs and pass the read/write partition test:\n%s", pattern, got)
		}
	}
}

// TestToolPartitionCoversEveryRegisteredTool keeps the read/write split honest against the server's actual registry. A tool added to registerTools() without being classified here would otherwise silently escape the coverage assertion above — and if it is a write, escape the printed approval advice too.
func TestToolPartitionCoversEveryRegisteredTool(t *testing.T) {
	classified := map[string]bool{}
	for _, n := range append(append([]string{}, readTools...), writeTools...) {
		if classified[n] {
			t.Fatalf("tool %s classified twice", n)
		}
		classified[n] = true
	}

	registered := mcp.ToolNames()
	if len(registered) != len(classified) {
		t.Errorf("server registers %d tools but %d are classified read/write", len(registered), len(classified))
	}
	for _, n := range registered {
		if !classified[n] {
			t.Errorf("registered tool %s is neither in readTools nor writeTools — classify it, and if it writes, add it to the printed approval globs", n)
		}
	}
	for n := range classified {
		found := false
		for _, r := range registered {
			if r == n {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("classified tool %s is no longer registered by the server", n)
		}
	}
}
