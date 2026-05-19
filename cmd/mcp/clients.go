package mcp

import (
	"fmt"
	"sort"
	"strings"
)

// mcpClient describes one MCP-capable host CLI that knows how to
// register an external MCP server in its own config. Each entry owns
// the argv shape because the three supported CLIs disagree on syntax:
//
//   - claude: `claude mcp add <name> <cmd> [args...]`
//   - codex:  `codex  mcp add <name> -- <cmd> [args...]`  (needs `--`)
//   - gemini: `gemini mcp add <name> <cmd> [args...]`
//
// Removal is uniform (`<cli> mcp remove <name>`), but we keep the
// builder per-client so any future divergence (e.g. scope flags) lives
// in one place.
type mcpClient struct {
	// Name is what the user types on the command line and what we
	// look up in `exec.LookPath`. It's also the binary name itself —
	// all three CLIs name their binary after their product.
	Name string

	// InstallHint is shown when the binary isn't on PATH. Mirrors the
	// pattern in internal/tools/tools.go so error messages stay
	// actionable.
	InstallHint string

	// ConfigPath is the on-disk file the user would hand-edit if they
	// can't (or won't) install the client CLI. Surfaced in the
	// PATH-miss error so the user knows *where* to paste ManualSnippet.
	ConfigPath string

	// ManualSnippet is the config-file fragment the user can paste by
	// hand if the client CLI isn't on PATH. Must be syntactically
	// valid in the target file's language (JSON for claude/gemini,
	// TOML for codex) — do not embed file-path labels as `//`
	// comments, those break strict JSON parsers (gemini in particular).
	// Put any prose framing in install.go's error message, not here.
	ManualSnippet string

	// AddArgv returns the argv (excluding argv[0]) passed to the
	// client binary to register a server named `serverName` whose
	// command is `serverCmd` with `serverArgs`. The everyapi install
	// path always calls this with serverName="everyapi",
	// serverCmd="everyapi", serverArgs=[]string{"mcp"}.
	AddArgv func(serverName, serverCmd string, serverArgs []string) []string

	// RemoveArgv returns the argv to unregister the server named
	// `serverName`.
	RemoveArgv func(serverName string) []string
}

// mcpClients is the registered set, keyed by the name the user types
// (`everyapi mcp install <client>`). Names are lowercase. Order in the
// help text comes from clientNames() below.
var mcpClients = map[string]*mcpClient{
	"claude": {
		Name:        "claude",
		InstallHint: "Install Claude Code: https://docs.claude.com/en/docs/claude-code/setup",
		ConfigPath:  "~/.claude/settings.json",
		ManualSnippet: `{
  "mcpServers": {
    "everyapi": { "command": "everyapi", "args": ["mcp"] }
  }
}`,
		AddArgv: func(name, cmd string, args []string) []string {
			// claude mcp add <name> <cmd> [args...]
			return append([]string{"mcp", "add", name, cmd}, args...)
		},
		RemoveArgv: func(name string) []string {
			return []string{"mcp", "remove", name}
		},
	},
	"codex": {
		Name:        "codex",
		InstallHint: "Install Codex CLI: https://github.com/openai/codex#installation",
		ConfigPath:  "~/.codex/config.toml",
		ManualSnippet: `[mcp_servers.everyapi]
command = "everyapi"
args = ["mcp"]`,
		AddArgv: func(name, cmd string, args []string) []string {
			// codex mcp add <name> -- <cmd> [args...]
			// The `--` separator is required: without it codex parses
			// the command as another flag.
			return append([]string{"mcp", "add", name, "--", cmd}, args...)
		},
		RemoveArgv: func(name string) []string {
			return []string{"mcp", "remove", name}
		},
	},
	"gemini": {
		Name:        "gemini",
		InstallHint: "Install Gemini CLI: https://github.com/google-gemini/gemini-cli#installation",
		ConfigPath:  "~/.gemini/settings.json",
		ManualSnippet: `{
  "mcpServers": {
    "everyapi": { "command": "everyapi", "args": ["mcp"] }
  }
}`,
		AddArgv: func(name, cmd string, args []string) []string {
			// gemini mcp add <name> <cmd> [args...]
			return append([]string{"mcp", "add", name, cmd}, args...)
		},
		RemoveArgv: func(name string) []string {
			return []string{"mcp", "remove", name}
		},
	},
}

// clientNames returns the registered client names in stable order
// (claude first, then codex, then gemini — matches the ordering in
// internal/tools/tools.go Names()).
func clientNames() []string {
	preferred := []string{"claude", "codex", "gemini"}
	seen := map[string]bool{}
	out := make([]string, 0, len(mcpClients))
	for _, n := range preferred {
		if _, ok := mcpClients[n]; ok {
			out = append(out, n)
			seen[n] = true
		}
	}
	// Any future additions get tacked on alphabetically so callers
	// don't have to remember to touch this slice.
	extras := []string{}
	for n := range mcpClients {
		if !seen[n] {
			extras = append(extras, n)
		}
	}
	sort.Strings(extras)
	return append(out, extras...)
}

// lookupClient resolves the user-facing client name to its registry
// entry, or returns an error listing the supported names. Used by
// Install and Uninstall.
func lookupClient(name string) (*mcpClient, error) {
	c, ok := mcpClients[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("unknown MCP client %q — supported: %s", name, strings.Join(clientNames(), ", "))
	}
	return c, nil
}
