package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestClientConfigPathHonorsCodexHome(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "managed-codex"))
	got := clientConfigPath(mcpClients["codex"])
	want := filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")
	if got != want {
		t.Fatalf("clientConfigPath(codex) = %q, want %q", got, want)
	}
	if got := clientConfigPath(mcpClients["claude"]); got != "~/.claude.json" {
		t.Fatalf("clientConfigPath(claude) = %q", got)
	}
}

func TestClientConfigPathHonorsClaudeConfigDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "managed-claude")
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if got, want := clientConfigPath(mcpClients["claude"]), filepath.Join(dir, ".claude.json"); got != want {
		t.Fatalf("clientConfigPath(claude) = %q, want %q", got, want)
	}
}

func TestClientConfigPathHonorsGeminiCLIHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "managed-gemini")
	t.Setenv("GEMINI_CLI_HOME", home)
	if got, want := clientConfigPath(mcpClients["gemini"]), filepath.Join(home, "settings.json"); got != want {
		t.Fatalf("clientConfigPath(gemini) = %q, want %q", got, want)
	}
}

func TestClientConfigPathHonorsLibrefangHome(t *testing.T) {
	// LIBREFANG_HOME relocates the daemon's data dir; the default ~/.librefang/config.toml hint would then name a file nothing reads.
	home := filepath.Join(t.TempDir(), "managed-librefang")
	t.Setenv("LIBREFANG_HOME", home)
	if got, want := clientConfigPath(mcpClients["librefang"]), filepath.Join(home, "config.toml"); got != want {
		t.Fatalf("clientConfigPath(librefang) = %q, want %q", got, want)
	}
}

func TestLookupClient(t *testing.T) {
	// Every registered client must resolve, and its argv builders must match its mode: shell-out targets need both, ManualOnly targets must have neither (install/uninstall return before calling them, and a non-nil builder would signal a shell-out path that doesn't exist).
	for _, name := range clientNames() {
		t.Run(name, func(t *testing.T) {
			c, err := lookupClient(name)
			if err != nil {
				t.Fatalf("lookupClient(%q): %v", name, err)
			}
			if c.Name != name {
				t.Errorf("Name = %q, want %q", c.Name, name)
			}
			if c.ManualOnly {
				if c.AddArgv != nil || c.RemoveArgv != nil {
					t.Errorf("ManualOnly client must not define AddArgv/RemoveArgv")
				}
				if c.ManualSnippet == "" || c.ConfigPath == "" {
					t.Errorf("ManualOnly client needs ConfigPath + ManualSnippet to print")
				}
				return
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
		// Error must list every supported name so the user has a recoverable path. If we add a client and forget to extend clientNames(), this assertion catches it.
		for _, name := range clientNames() {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not mention %q", err, name)
			}
		}
	})
}

func TestClientAddArgv(t *testing.T) {
	// The codex CLI requires a `--` separator before the launched command; claude and gemini take it positionally. Lock that down: regressions here mean `everyapi mcp install <client>` silently registers the wrong thing.
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
	cases := []struct {
		name string
		want []string
	}{
		{"claude", []string{"mcp", "remove", "everyapi"}},
		{"codex", []string{"mcp", "remove", "everyapi"}},
		// Gemini defaults removal to project scope, while install writes user scope. Omitting this flag reports success but leaves the registration.
		{"gemini", []string{"mcp", "remove", "--scope", "user", "everyapi"}},
	}
	for _, tc := range cases {
		c, _ := lookupClient(tc.name)
		got := c.RemoveArgv("everyapi")
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s RemoveArgv = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestClientNamesOrder(t *testing.T) {
	// Preferred order is locked: claude, codex, gemini. The install usage text and the unknown-client error both depend on this ordering being stable. librefang is not in the preferred slice, so it lands in the alphabetical `extras` tail — which is the documented behaviour for additions.
	want := []string{"claude", "codex", "gemini", "librefang"}
	got := clientNames()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("clientNames() = %v, want %v", got, want)
	}
}

func TestClientConfigPath(t *testing.T) {
	// ConfigPath is read by install.go's PATH-miss error message — must be populated for every client so the user knows where to paste ManualSnippet.
	want := map[string]string{
		"claude":    "~/.claude.json",
		"codex":     "~/.codex/config.toml",
		"gemini":    "~/.gemini/settings.json",
		"librefang": "~/.librefang/config.toml",
	}
	for name, path := range want {
		c, _ := lookupClient(name)
		if c.ConfigPath != path {
			t.Errorf("%s ConfigPath = %q, want %q", name, c.ConfigPath, path)
		}
	}
}

func TestClientManualSnippet(t *testing.T) {
	// The snippet is what the user pastes into ConfigPath when the client CLI isn't on PATH. Two regressions to guard against:
	//   1. The snippet doesn't parse in its target language (e.g. an earlier version embedded `// path` JSON-illegal comments).
	//   2. A client's snippet uses the wrong language (codex with JSON, or claude/gemini with TOML).

	// JSON clients: parse with encoding/json. If the snippet isn't valid JSON, gemini's strict parser will reject what we tell the user to paste — so we mirror that strictness here.
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

	// codex: stdlib has no TOML parser and we don't pull in a dep just for this. Settle for structural assertions on the section header and key/value lines — enough to catch a copy-paste of a JSON template into the codex slot.
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

	// librefang: unlike codex, this snippet IS the whole deliverable — it is never verified by a successful shell-out, because there is no shell-out. So decode it for real (BurntSushi is already a direct dependency) into the shape librefang's McpServerConfigEntry deserializes, and assert every field the daemon reads. A structural substring check would pass on TOML that parses into the wrong shape — e.g. command/args hoisted to the top level instead of nested under [transport], which is the exact mistake in librefang's own published example.
	t.Run("librefang/parses-as-mcp-servers-entry", func(t *testing.T) {
		c, _ := lookupClient("librefang")
		var got struct {
			McpServers []struct {
				Name      string `toml:"name"`
				Transport struct {
					Type    string   `toml:"type"`
					Command string   `toml:"command"`
					Args    []string `toml:"args"`
				} `toml:"transport"`
				TimeoutSecs int      `toml:"timeout_secs"`
				Env         []string `toml:"env"`
			} `toml:"mcp_servers"`
		}
		if _, err := toml.Decode(c.ManualSnippet, &got); err != nil {
			t.Fatalf("ManualSnippet does not parse as TOML: %v\n%s", err, c.ManualSnippet)
		}
		if len(got.McpServers) != 1 {
			t.Fatalf("want exactly one [[mcp_servers]] entry, got %d:\n%s", len(got.McpServers), c.ManualSnippet)
		}
		e := got.McpServers[0]
		if e.Name != "everyapi" {
			t.Errorf("name = %q, want \"everyapi\"", e.Name)
		}
		// transport is a nested table, NOT top-level command/args keys — librefang's McpServerConfigEntry has no top-level command field, so the flat form silently registers a server that can never start.
		if e.Transport.Type != "stdio" {
			t.Errorf("transport.type = %q, want \"stdio\"", e.Transport.Type)
		}
		if e.Transport.Command != "everyapi" {
			t.Errorf("transport.command = %q, want \"everyapi\"", e.Transport.Command)
		}
		if !reflect.DeepEqual(e.Transport.Args, []string{"mcp"}) {
			t.Errorf("transport.args = %v, want [mcp]", e.Transport.Args)
		}
		if e.TimeoutSecs <= 0 {
			t.Errorf("timeout_secs = %d, want a positive value", e.TimeoutSecs)
		}
		// env is a list of strings ("KEY=value" or a bare "KEY" pulled from the daemon's environment), not a table. Empty is correct: the daemon passes HOME through, so `everyapi mcp` finds its own credentials.
		if len(e.Env) != 0 {
			t.Errorf("env = %v, want empty (credentials come from HOME)", e.Env)
		}
	})

	// The JSON shape belongs to claude/gemini; a copy-paste into the librefang slot would parse as TOML-invalid, but assert the intent too.
	t.Run("librefang/no-json-leak", func(t *testing.T) {
		c, _ := lookupClient("librefang")
		if strings.Contains(c.ManualSnippet, `"mcpServers"`) {
			t.Errorf("librefang ManualSnippet contains JSON `mcpServers` key:\n%s", c.ManualSnippet)
		}
	})
}
