// relaya — CLI for the Relaya AI API gateway.
//
// Covers the V1 buyer onboarding flow (login, logout, status, use)
// AND hosts the MCP server as the `mcp` subcommand — same Go
// module, single binary, one install. See README.md for the full
// command surface and the design rationale.
package main

import (
	"fmt"
	"os"

	"github.com/relaya-ai/relaya-ai/cmd"
	"github.com/relaya-ai/relaya-ai/cmd/mcp"
)

const usage = `relaya — Relaya CLI

USAGE
  relaya <command> [flags]

COMMANDS
  login           Authenticate this device with Relaya
  logout          Remove this device's credentials
  status          Show current quota, usage, and balance
  use <tool>      Launch a third-party CLI (claude / codex / gemini) routed through Relaya
                  (--group/--channel <name>: route via the key bound to that group;
                   bare --group/--channel: pick the group interactively)
  seller <sub>    Channel-marketplace seller commands:
                    seller list                      List your mounted channels
                    seller withdraw [--quota N]      Move pending earnings to main balance
                    seller add-key  --type/--name/--key/--models …
                                                     Mount a plain-API-key channel
                    seller setup                     Interactive mount wizard
  mcp                    Run the MCP server on stdio (spawned by Claude Code / Codex / Gemini)
  mcp install [client]   Auto-register relaya as an MCP server (client: claude|codex|gemini; default claude)
  mcp uninstall [client] Remove the MCP registration created by 'mcp install' (default claude)
  update          Check for a newer release and run the matching upgrade
                  (brew / go install — auto-detected from binary path)
                  (--check: silent compare, exits 1 if outdated;
                   --dry-run: print the command instead of running it)
  version         Print the build version
  help            Show this message

Run 'relaya <command> --help' for command-specific flags.

MCP server: quickest path on macOS / Linux with one of the AI CLIs installed:
  relaya mcp install            # default: claude
  relaya mcp install codex
  relaya mcp install gemini
That runs "<client> mcp add relaya relaya mcp" under the hood. After it,
the client spawns "relaya mcp" on demand — ask the AI "what's my
Relaya balance?" and it'll invoke the relaya_status tool.

Manual config (other MCP clients, or to opt out of the helper):
  { "command": "relaya", "args": ["mcp"] }
The server reads JSON-RPC from stdin, writes to stdout, logs to stderr.
Auth is read from ~/.config/relaya/credentials.json — run 'relaya login' first.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	name := os.Args[1]
	args := os.Args[2:]

	var err error
	switch name {
	case "login":
		err = cmd.Login(args)
	case "logout":
		err = cmd.Logout(args)
	case "status":
		err = cmd.Status(args)
	case "use":
		err = cmd.Use(args)
	case "seller":
		err = cmd.Seller(args)
	case "mcp":
		// `relaya mcp` (no subcommand) → run the JSON-RPC server on
		// stdio for an MCP client to drive.
		// `relaya mcp install` → register us with Claude Code via
		// `claude mcp add`, so the user doesn't have to hand-edit
		// settings.json.
		// `relaya mcp uninstall` → inverse via `claude mcp remove`.
		if len(args) > 0 {
			switch args[0] {
			case "install":
				err = mcp.Install(args[1:])
			case "uninstall":
				err = mcp.Uninstall(args[1:])
			default:
				fmt.Fprintf(os.Stderr, "unknown 'mcp' subcommand %q. Try 'relaya mcp install' to register, 'relaya mcp uninstall' to remove, or 'relaya mcp' (no args) to run the server.\n", args[0])
				os.Exit(2)
			}
		} else {
			err = mcp.Run(os.Stdin, os.Stdout, os.Stderr)
		}
	case "update":
		err = cmd.Update(args)
	case "version", "--version", "-v":
		err = cmd.Version(args)
	case "help", "--help", "-h":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}
