package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestLookupClient(t *testing.T) {
	for _, name := range []string{"claude", "codex", "gemini"} {
		t.Run(name, func(t *testing.T) {
			c, err := lookupClient(name)
			if err != nil {
				t.Fatalf("lookupClient(%q): %v", name, err)
			}
			if c.Name != name {
				t.Errorf("Name = %q, want %q", c.Name, name)
			}
			if c.AddArgv == nil || c.RemoveArgv == nil {
				t.Errorf("nil argv builder")
			}
		})
	}

	t.Run("case-insensitive", func(t *testing.T) {
		c, err := lookupClient("CLAUDE")
		if err != nil || c.Name != "claude" {
			t.Errorf("uppercase lookup failed: %v / %v", c, err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		_, err := lookupClient("cursor")
		if err == nil {
			t.Fatal("expected error for unknown client")
		}
		// Error must list every supported name so the user has a
		// recoverable path. If we add a client and forget to extend
		// clientNames(), this assertion catches it.
		for _, name := range clientNames() {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not mention %q", err, name)
			}
		}
	})
}

func TestClientAddArgv(t *testing.T) {
	// The codex CLI requires a `--` separator before the launched
	// command; claude and gemini take it positionally. Lock that down:
	// regressions here mean `everyapi mcp install <client>` silently
	// registers the wrong thing.
	cases := []struct {
		client string
		want   []string
	}{
		{"claude", []string{"mcp", "add", "--scope", "user", "everyapi", "everyapi", "mcp"}},
		{"codex", []string{"mcp", "add", "everyapi", "--", "everyapi", "mcp"}},
		{"gemini", []string{"mcp", "add", "--scope", "user", "everyapi", "everyapi", "mcp"}},
	}
	for _, tc := range cases {
		t.Run(tc.client, func(t *testing.T) {
			c, err := lookupClient(tc.client)
			if err != nil {
				t.Fatal(err)
			}
			got := c.AddArgv("everyapi", "everyapi", []string{"mcp"})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("AddArgv = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClientRemoveArgv(t *testing.T) {
	// All three clients currently take `mcp remove <name>` — but the
	// builder is per-client so this stays correct if one diverges.
	for _, name := range []string{"claude", "codex", "gemini"} {
		c, _ := lookupClient(name)
		got := c.RemoveArgv("everyapi")
		want := []string{"mcp", "remove", "everyapi"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s RemoveArgv = %v, want %v", name, got, want)
		}
	}
}

func TestClientNamesOrder(t *testing.T) {
	// Preferred order is locked: claude, codex, gemini. The install
	// usage text and the unknown-client error both depend on this
	// ordering being stable.
	want := []string{"claude", "codex", "gemini"}
	got := clientNames()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("clientNames() = %v, want %v", got, want)
	}
}

func TestClientConfigPath(t *testing.T) {
	// ConfigPath is read by install.go's PATH-miss error message —
	// must be populated for every client so the user knows where to
	// paste ManualSnippet.
	want := map[string]string{
		"claude": "~/.claude.json",
		"codex":  "~/.codex/config.toml",
		"gemini": "~/.gemini/settings.json",
	}
	for name, path := range want {
		c, _ := lookupClient(name)
		if c.ConfigPath != path {
			t.Errorf("%s ConfigPath = %q, want %q", name, c.ConfigPath, path)
		}
	}
}

func TestClientManualSnippet(t *testing.T) {
	// The snippet is what the user pastes into ConfigPath when the
	// client CLI isn't on PATH. Two regressions to guard against:
	//   1. The snippet doesn't parse in its target language (e.g. an
	//      earlier version embedded `// path` JSON-illegal comments).
	//   2. A client's snippet uses the wrong language (codex with
	//      JSON, or claude/gemini with TOML).

	// JSON clients: parse with encoding/json. If the snippet isn't
	// valid JSON, gemini's strict parser will reject what we tell the
	// user to paste — so we mirror that strictness here.
	for _, name := range []string{"claude", "gemini"} {
		t.Run(name+"/parses-as-json", func(t *testing.T) {
			c, _ := lookupClient(name)
			var got struct {
				McpServers map[string]struct {
					Command string   `json:"command"`
					Args    []string `json:"args"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal([]byte(c.ManualSnippet), &got); err != nil {
				t.Fatalf("ManualSnippet does not parse as JSON: %v\n%s", err, c.ManualSnippet)
			}
			r, ok := got.McpServers["everyapi"]
			if !ok {
				t.Fatalf("ManualSnippet missing mcpServers.everyapi:\n%s", c.ManualSnippet)
			}
			if r.Command != "everyapi" || !reflect.DeepEqual(r.Args, []string{"mcp"}) {
				t.Errorf("ManualSnippet everyapi entry = {%q, %v}, want {\"everyapi\", [\"mcp\"]}", r.Command, r.Args)
			}
		})
	}

	// codex: stdlib has no TOML parser and we don't pull in a dep just
	// for this. Settle for structural assertions on the section
	// header and key/value lines — enough to catch a copy-paste of a
	// JSON template into the codex slot.
	t.Run("codex/structure", func(t *testing.T) {
		c, _ := lookupClient("codex")
		for _, frag := range []string{"[mcp_servers.everyapi]", `command = "everyapi"`, `args = ["mcp"]`} {
			if !strings.Contains(c.ManualSnippet, frag) {
				t.Errorf("codex ManualSnippet missing %q:\n%s", frag, c.ManualSnippet)
			}
		}
		if strings.Contains(c.ManualSnippet, `"mcpServers"`) {
			t.Errorf("codex ManualSnippet contains JSON `mcpServers` key:\n%s", c.ManualSnippet)
		}
	})

	// Cross-check: JSON clients must NOT carry a TOML section header.
	for _, name := range []string{"claude", "gemini"} {
		t.Run(name+"/no-toml-leak", func(t *testing.T) {
			c, _ := lookupClient(name)
			if strings.Contains(c.ManualSnippet, "[mcp_servers.") {
				t.Errorf("%s ManualSnippet contains TOML section header:\n%s", name, c.ManualSnippet)
			}
		})
	}
}
