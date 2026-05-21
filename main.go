// everyapi — CLI for the EveryAPI AI API gateway.
//
// Covers the V1 buyer onboarding flow (login, logout, status, use)
// AND hosts the MCP server as the `mcp` subcommand — same Go
// module, single binary, one install. See README.md for the full
// command surface and the design rationale.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/cmd"
	"github.com/everyapi-ai/everyapi-ai/cmd/admin"
	"github.com/everyapi-ai/everyapi-ai/cmd/edge"
	mcpcmd "github.com/everyapi-ai/everyapi-ai/cmd/mcp"
	"github.com/everyapi-ai/everyapi-ai/cmd/proxy"
	"github.com/everyapi-ai/everyapi-ai/cmd/seller"
	"github.com/everyapi-ai/everyapi-ai/internal/mcp"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi — EveryAPI CLI

USAGE
  everyapi <command> [flags]

COMMANDS
  login           Authenticate this device with EveryAPI
  logout          Remove this device's credentials
  status          Show current quota, usage, and balance
  topup           Open the wallet top-up page (anti-phishing verification phrase)
  use <tool>      Launch a third-party CLI (claude / codex / gemini) via EveryAPI
  seller <sub>    Channel-marketplace seller commands
__ADMIN_BLOCK__
  edge <sub>      BYO-GPU supplier agent (docker + ollama)
  proxy <sub>     Local sanitizer proxy (privacy filter for SDK requests)
  mcp [sub]       MCP server for AI CLIs (Claude Code / Codex / Gemini)
  update          Check for a newer release and run the matching upgrade
  version         Print the build version
  help            Show this message

Run 'everyapi <command> help' for subcommand details and flags
(e.g. 'everyapi seller help', 'everyapi edge help').
`

// command is a single top-level subcommand. The registry below
// replaces the older switch/case dispatch — adding a command is now
// one line in `commands`, and the help-flag handling stays uniform.
type command struct {
	name string
	// aliases lets `version` also answer to `--version` / `-v` without
	// a special case in main. The principle is "alias = synonym", not
	// "alias = different behavior".
	aliases []string
	run     func(args []string) error
}

// commands is the registered set, in the order they appear in the
// help text. main() walks this slice (not a map) so the lookup order
// matches the documented order — keeps the "which command runs when
// two names conflict" question impossible.
var commands = []command{
	{name: "login", run: cmd.Login},
	{name: "logout", run: cmd.Logout},
	{name: "status", run: cmd.Status},
	{name: "topup", run: cmd.Topup},
	{name: "use", run: cmd.Use},
	{name: "seller", run: seller.Run},
	{name: "edge", run: edge.Run},
	{name: "admin", run: admin.Run},
	{name: "proxy", run: proxy.Run},
	{name: "mcp", run: runMCP},
	{name: "update", run: cmd.Update},
	{name: "version", aliases: []string{"--version", "-v"}, run: cmd.Version},
}

// adminUsageBlock is appended to the base usage by renderUsage when
// the logged-in user has admin role. Keeping it out of the base
// string means non-admin users don't see commands they can't run —
// the backend still rejects them with a 403 if a non-admin types the
// command anyway, but the help output stays clean.
const adminUsageBlock = `  admin <sub>     Operator commands (admin role required)
`

// adminBlockSentinel is the placeholder inside the static `usage`
// string that renderUsage substitutes. A sentinel rather than a
// text-anchor (e.g. strings.Index(usage, "  proxy <sub>")) is more
// robust to future help-text reorders — the placeholder moves with
// the help block, so reordering the COMMANDS section can't silently
// orphan the admin block at the end.
const adminBlockSentinel = "__ADMIN_BLOCK__\n"

// renderUsage returns the usage block, substituting adminUsageBlock
// into the sentinel iff the cached credential's Role indicates an
// admin user. Non-admin / unauthenticated callers see the sentinel
// stripped (no leak). Falls through to plain strip on any
// credential-load error.
func renderUsage() string {
	creds, err := config.Load()
	if err != nil || !creds.IsAdmin() {
		return strings.Replace(usage, adminBlockSentinel, "", 1)
	}
	return strings.Replace(usage, adminBlockSentinel, adminUsageBlock, 1)
}

// lookup resolves a CLI-typed name (incl. aliases) to its command.
func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
		for _, a := range c.aliases {
			if a == name {
				return c, true
			}
		}
	}
	return command{}, false
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(renderUsage())
		os.Exit(2)
	}
	name := os.Args[1]
	args := os.Args[2:]

	if name == "help" || name == "--help" || name == "-h" {
		fmt.Print(renderUsage())
		return
	}

	c, ok := lookup(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, renderUsage())
		os.Exit(2)
	}

	if err := c.run(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

// runMCP is the `mcp` family dispatcher. Three shapes:
//
//	everyapi mcp                       → run the JSON-RPC server on stdio
//	everyapi mcp install [client]      → register via `<client> mcp add`
//	everyapi mcp uninstall [client]    → unregister via `<client> mcp remove`
//
// Kept here (not in cmd/mcp) so the server entry point (internal/mcp)
// and the install/uninstall handlers (cmd/mcp) don't have to know
// about each other — main wires both.
func runMCP(args []string) error {
	if len(args) == 0 {
		return mcp.Run(os.Stdin, os.Stdout, os.Stderr)
	}
	switch args[0] {
	case "install":
		return mcpcmd.Install(args[1:])
	case "uninstall":
		return mcpcmd.Uninstall(args[1:])
	case "help", "--help", "-h":
		// `everyapi mcp --help` used to fall through to the unknown-
		// subcommand branch and exit 2 with a confusing message;
		// keep parity with every other command's help-flag handling.
		fmt.Print(renderUsage())
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown 'mcp' subcommand %q. Try 'everyapi mcp install' to register, 'everyapi mcp uninstall' to remove, or 'everyapi mcp' (no args) to run the server.\n", args[0])
		os.Exit(2)
		return nil // unreachable
	}
}
