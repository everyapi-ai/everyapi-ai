package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
)

// clientConfigPath returns the path the client CLI actually uses. Codex
// supports CODEX_HOME and EveryAPI's managed Codex launcher sets it, so a
// hard-coded ~/.codex hint can point at a completely unrelated file.
func clientConfigPath(c *mcpClient) string {
	if c != nil {
		switch c.Name {
		case "claude":
			if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
				return filepath.Join(dir, ".claude.json")
			}
		case "codex":
			if home := os.Getenv("CODEX_HOME"); home != "" {
				return filepath.Join(home, "config.toml")
			}
		case "gemini":
			if home := os.Getenv("GEMINI_CLI_HOME"); home != "" {
				return filepath.Join(home, "settings.json")
			}
		case "librefang":
			// LIBREFANG_HOME relocates the daemon's whole data dir, config.toml
			// included. Same hazard as CODEX_HOME: a hard-coded ~/.librefang
			// hint would name a file the daemon never reads.
			if home := os.Getenv("LIBREFANG_HOME"); home != "" {
				return filepath.Join(home, "config.toml")
			}
		}
	}
	if c == nil {
		return ""
	}
	return c.ConfigPath
}

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
		"claude":    "Claude Code (Anthropic)",
		"codex":     "Codex CLI (OpenAI ChatGPT subscription)",
		"gemini":    "Gemini CLI (Google)",
		"librefang": "LibreFang Agent OS (daemon — prints config to paste)",
	}
	labels := make([]string, len(names))
	for i, n := range names {
		labels[i] = fmt.Sprintf("%-9s — %s", n, displayName[n])
	}
	idx, err := cliprompt.Pick(fmt.Sprintf("mcp %s — pick a client:", op), labels)
	if err != nil {
		return "", err
	}
	return names[idx], nil
}

// mcpClient describes one MCP-capable host that can run `everyapi mcp`
// as a server. Two shapes live in this registry.
//
// Shell-out targets own the argv because the CLIs disagree on syntax:
//
//   - claude: `claude mcp add <name> <cmd> [args...]`
//   - codex:  `codex  mcp add <name> -- <cmd> [args...]`  (needs `--`)
//   - gemini: `gemini mcp add <name> <cmd> [args...]`
//
// Removal is client-specific too: Gemini defaults removal to project scope,
// while EveryAPI installs in user scope, so its remove argv must repeat the
// scope explicitly.
//
// ManualOnly targets (librefang) have no usable add/remove verb and instead
// print ConfigPath + ManualSnippet for the user to paste. See the ManualOnly
// field for why.
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

	// ManualOnly marks a target whose registration we print rather than
	// perform. Install and Uninstall emit ConfigPath + ManualSnippet and
	// return without shelling out; AddArgv / RemoveArgv stay nil. Status
	// still probes via ListArgv, since reading is safe.
	//
	// Set for librefang, for three independent reasons — any one of which
	// would be enough on its own:
	//
	//  1. No add verb accepts a command. `librefang mcp add <id>` takes a
	//     catalog id and nothing else: it looks the id up in
	//     ~/.librefang/mcp/catalog/ and fails if it's absent. There is no
	//     `librefang mcp add <name> <cmd> [args...]` form to build argv for,
	//     so AddArgv has nothing to express. Until an "everyapi" catalog
	//     entry ships, the command cannot succeed at all.
	//  2. Its errors are invisible to our classifiers. librefang prints
	//     errors to stdout, not stderr, and localizes them (en / zh-CN / uk /
	//     ko). isAlreadyRegistered and isNotRegistered read captured stderr
	//     and match English phrases, so both would see an empty string: a
	//     repeat install would hard-fail instead of reporting the idempotent
	//     no-op, and a repeat uninstall would do the same in every non-English
	//     locale.
	//  3. Writing config.toml ourselves is worse than not writing it. It is
	//     the daemon's whole configuration — providers, keys, channels, cron
	//     — not an MCP-only file, and a running daemon reads it live. Our TOML
	//     library round-trips through map[string]any, which drops every
	//     comment and reorders keys, so an "upsert" would silently rewrite the
	//     user's hand-tuned file to add four lines.
	//
	// Printing is also honest about the second step: registering the server
	// daemon-wide does not grant it to any agent. Each agent's mcp_servers
	// allowlist must name it, and an empty list means none.
	ManualOnly bool

	// PostInstallNote is extra prose appended to the manual instructions,
	// after ConfigPath + ManualSnippet. Only read for ManualOnly targets,
	// where the paste alone is not the whole job.
	PostInstallNote string
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
	// LibreFang is a daemon, not a CLI that owns its own MCP registry: its
	// servers live in ~/.librefang/config.toml under [[mcp_servers]], the
	// same file that holds every other daemon setting. ManualOnly (see the
	// field doc) — we print the block, the user pastes it.
	"librefang": {
		Name:        "librefang",
		InstallHint: "Install LibreFang: curl -fsSL https://librefang.ai/install.sh | sh",
		ConfigPath:  "~/.librefang/config.toml",
		ManualOnly:  true,
		// No `env` entries: the daemon passes HOME through to stdio MCP
		// children, so `everyapi mcp` finds ~/.config/everyapi/credentials.json
		// on its own. template_id is deliberately omitted — it asserts the
		// entry came from the MCP catalog, which a hand-pasted block did not.
		ManualSnippet: `[[mcp_servers]]
name = "everyapi"
transport = { type = "stdio", command = "everyapi", args = ["mcp"] }
timeout_secs = 30
env = []`,
		PostInstallNote: `Then grant it to each agent that should see the tools — a daemon-wide
server is NOT automatically available to agents. In
~/.librefang/workspaces/agents/<agent>/agent.toml, add "everyapi" to the
allowlist (an empty mcp_servers = [] means NO servers, not all):

  mcp_servers = ["everyapi"]

Restart the daemon to pick up both edits, then ask the agent "what's my
EveryAPI balance?" — it will invoke mcp_everyapi_everyapi_status.

Consider gating the write tools while you're in config.toml — some move
money (everyapi_seller_withdraw) or upload a plaintext API key
(everyapi_seller_add_key). config.toml normally already has an [approval]
section, so APPEND these four patterns to the existing require_approval
list rather than pasting a second [approval] table (duplicate tables are a
TOML error, and replacing the list would drop the defaults that gate
shell_exec):

  "mcp_everyapi_everyapi_seller_add_*"
  "mcp_everyapi_everyapi_seller_withdraw"
  "mcp_everyapi_everyapi_admin_marketplace_set"
  "mcp_everyapi_everyapi_edge_remove"

They cover all 8 write tools and leave the 7 read tools ungated, so a
balance check still runs without a prompt. A blanket "mcp_everyapi_*"
would also gate everyapi_status and stall the unattended balance-check
use case. The names are doubly prefixed on purpose: LibreFang namespaces
every MCP tool as mcp_<server>_<tool> and our tools are already named
everyapi_*, so the two compose without de-duplication.

Note that require_approval only pauses for a human — it is not a deny —
and several settings switch it off entirely: a sender in trusted_senders
bypasses it for any tool LibreFang does not classify as high-risk (no MCP
tool is), a hand-tagged agent auto-approves every tool, and
cache_approvals_per_session (default true) means one approval covers the
rest of the session. Do not rely on these globs alone for an untrusted
sender. See https://librefang.ai/integrations/mcp-a2a for the full list.`,
		// Read-only, so it is safe to run: `librefang mcp list` prints one
		// row per configured server with the name in the first column, which
		// is exactly what listOutputHasServer matches.
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
