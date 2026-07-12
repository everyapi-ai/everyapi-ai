package mcp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
)

// resolveClient picks which MCP client to operate on. Three paths,
// in priority order:
//
//  1. Explicit argv (`everyapi mcp install codex`) — args[0] wins.
//  2. Interactive TTY — show a picker over every known client so
//     the user gets a multi-row choice instead of "default claude".
//  3. Non-TTY with no argv — fall back to the historical "claude"
//     default so scripted invocations don't suddenly break.
//
// op is the verb being resolved ("install" / "uninstall"), surfaced
// in the picker title so the user can tell which side of the wizard
// they're on if they got here from the launcher's sub-picker.
func resolveClient(args []string, op string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return args[0], nil
	}
	if !cliprompt.IsInteractive() {
		return "claude", nil
	}
	names := clientNames()
	// Friendly labels are inlined here rather than carried on the
	// mcpClient struct: they're picker-UI text, not protocol data,
	// and changing them shouldn't ripple through install/uninstall.
	displayName := map[string]string{
		"claude": "Claude Code (Anthropic)",
		"codex":  "Codex CLI (OpenAI ChatGPT subscription)",
		"gemini": "Gemini CLI (Google)",
	}
	labels := make([]string, len(names))
	for i, n := range names {
		labels[i] = fmt.Sprintf("%-8s — %s", n, displayName[n])
	}
	idx, err := cliprompt.Pick(fmt.Sprintf("mcp %s — pick a client:", op), labels)
	if err != nil {
		return "", err
	}
	return names[idx], nil
}

// mcpClient describes one MCP-capable host CLI that knows how to
// register an external MCP server in its own config. Each entry owns
// the argv shape because the three supported CLIs disagree on syntax:
//
//   - claude: `claude mcp add <name> <cmd> [args...]`
//   - codex:  `codex  mcp add <name> -- <cmd> [args...]`  (needs `--`)
//   - gemini: `gemini mcp add <name> <cmd> [args...]`
//
// Removal is client-specific too: Gemini defaults removal to project scope,
// while EveryAPI installs in user scope, so its remove argv must repeat the
// scope explicitly.
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

	// ListArgv returns the argv to enumerate every MCP server
	// already registered with this client. `everyapi mcp status`
	// runs it and looks for "everyapi" in the combined stdout +
	// stderr — substring rather than structured parse because each
	// client formats its list differently and the output drifts
	// across versions, but the server name is always present.
	ListArgv func() []string

	// ProbeArgv, when set, checks one server by name without enumerating or
	// health-checking unrelated servers. Exit 0 means registered; a normal
	// non-zero exit means the named server is absent. This avoids commands such
	// as `claude mcp list`, which contacts every configured remote MCP.
	ProbeArgv func(serverName string) []string
}

// mcpClients is the registered set, keyed by the name the user types
// (`everyapi mcp install <client>`). Names are lowercase. Order in the
// help text comes from clientNames() below.
var mcpClients = map[string]*mcpClient{
	"claude": {
		Name:        "claude",
		InstallHint: "Install Claude Code: https://docs.claude.com/en/docs/claude-code/setup",
		// User-scope MCP servers live in ~/.claude.json (top-level
		// "mcpServers"), NOT ~/.claude/settings.json — Claude Code does
		// not read MCP servers from settings.json.
		ConfigPath: "~/.claude.json",
		ManualSnippet: `{
  "mcpServers": {
    "everyapi": { "command": "everyapi", "args": ["mcp"] }
  }
}`,
		AddArgv: func(name, cmd string, args []string) []string {
			// claude mcp add --scope user <name> <cmd> [args...]
			// Without --scope, `claude mcp add` defaults to local
			// (per-project, keyed to cwd) scope, so the registration only
			// takes effect when Claude Code is launched from the same
			// directory `everyapi mcp install` ran in. User scope makes it
			// global (and lets `mcp status` see it from any directory).
			return append([]string{"mcp", "add", "--scope", "user", name, cmd}, args...)
		},
		RemoveArgv: func(name string) []string {
			return []string{"mcp", "remove", name}
		},
		ListArgv: func() []string { return []string{"mcp", "list"} },
		ProbeArgv: func(name string) []string {
			return []string{"mcp", "get", name}
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
		ListArgv: func() []string { return []string{"mcp", "list"} },
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
			// gemini mcp add --scope user <name> <cmd> [args...]
			// Same reasoning as claude: without --scope the server is
			// registered in project scope (cwd-local) rather than the
			// user's global gemini config.
			return append([]string{"mcp", "add", "--scope", "user", name, cmd}, args...)
		},
		RemoveArgv: func(name string) []string {
			return []string{"mcp", "remove", "--scope", "user", name}
		},
		ListArgv: func() []string { return []string{"mcp", "list"} },
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
