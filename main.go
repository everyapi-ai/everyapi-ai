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

	"github.com/everyapi-ai/everyapi-ai/cmd"
	mcpcmd "github.com/everyapi-ai/everyapi-ai/cmd/mcp"
	"github.com/everyapi-ai/everyapi-ai/cmd/proxy"
	"github.com/everyapi-ai/everyapi-ai/cmd/seller"
	"github.com/everyapi-ai/everyapi-ai/internal/mcp"
)

const usage = `everyapi — EveryAPI CLI

USAGE
  everyapi <command> [flags]

COMMANDS
  login           Authenticate this device with EveryAPI
  logout          Remove this device's credentials
  status          Show current quota, usage, and balance
  topup           Open the wallet top-up page (anti-phishing verification phrase)
  use <tool>      Launch a third-party CLI (claude / codex / gemini) routed through EveryAPI
                  (--group/--channel <name>: route via the key bound to that group;
                   bare --group/--channel: pick the group interactively)
  seller <sub>    Channel-marketplace seller commands:
                    seller list                      List your mounted channels
                    seller withdraw [--quota N]      Move pending earnings to main balance
                    seller add-key  --type/--name/--key/--models …
                                                     Mount a plain-API-key channel
                    seller setup                     Interactive mount wizard
  proxy <sub>     Local sanitizer proxy (privacy filter for SDK requests):
                    proxy start [--listen … --upstream …]   Run in the foreground
                    proxy status                            Show running stats
  mcp                    Run the MCP server on stdio (spawned by Claude Code / Codex / Gemini)
  mcp install [client]   Auto-register everyapi as an MCP server (client: claude|codex|gemini; default claude)
  mcp uninstall [client] Remove the MCP registration created by 'mcp install' (default claude)
  update          Check for a newer release and run the matching upgrade
                  (brew / go install — auto-detected from binary path)
                  (--check: silent compare, exits 1 if outdated;
                   --dry-run: print the command instead of running it)
  version         Print the build version
  help            Show this message

Run 'everyapi <command> --help' for command-specific flags.

MCP server: quickest path on macOS / Linux with one of the AI CLIs installed:
  everyapi mcp install            # default: claude
  everyapi mcp install codex
  everyapi mcp install gemini
That runs "<client> mcp add everyapi everyapi mcp" under the hood. After it,
the client spawns "everyapi mcp" on demand — ask the AI "what's my
EveryAPI balance?" and it'll invoke the everyapi_status tool.

Manual config (other MCP clients, or to opt out of the helper):
  { "command": "everyapi", "args": ["mcp"] }
The server reads JSON-RPC from stdin, writes to stdout, logs to stderr.
Auth is read from ~/.config/everyapi/credentials.json — run 'everyapi login' first.
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
	{name: "proxy", run: proxy.Run},
	{name: "mcp", run: runMCP},
	{name: "update", run: cmd.Update},
	{name: "version", aliases: []string{"--version", "-v"}, run: cmd.Version},
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
		fmt.Print(usage)
		os.Exit(2)
	}
	name := os.Args[1]
	args := os.Args[2:]

	if name == "help" || name == "--help" || name == "-h" {
		fmt.Print(usage)
		return
	}

	c, ok := lookup(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, usage)
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
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown 'mcp' subcommand %q. Try 'everyapi mcp install' to register, 'everyapi mcp uninstall' to remove, or 'everyapi mcp' (no args) to run the server.\n", args[0])
		os.Exit(2)
		return nil // unreachable
	}
}
