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
	"github.com/everyapi-ai/everyapi-ai/cmd/mcp"
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
	case "topup":
		err = cmd.Topup(args)
	case "use":
		err = cmd.Use(args)
	case "seller":
		err = cmd.Seller(args)
	case "proxy":
		err = cmd.Proxy(args)
	case "mcp":
		// `everyapi mcp` (no subcommand) → run the JSON-RPC server on
		// stdio for an MCP client to drive.
		// `everyapi mcp install` → register us with Claude Code via
		// `claude mcp add`, so the user doesn't have to hand-edit
		// settings.json.
		// `everyapi mcp uninstall` → inverse via `claude mcp remove`.
		if len(args) > 0 {
			switch args[0] {
			case "install":
				err = mcp.Install(args[1:])
			case "uninstall":
				err = mcp.Uninstall(args[1:])
			case "help", "--help", "-h":
				// Match the top-level `help` / `--help` / `-h` and
				// the `everyapi seller help` flag handling. Before this
				// case, `everyapi mcp --help` fell into the default
				// branch and exited 2 with "unknown 'mcp' subcommand
				// "--help"" — confusing for a flag every other
				// command accepts.
				fmt.Print(usage)
				return
			default:
				fmt.Fprintf(os.Stderr, "unknown 'mcp' subcommand %q. Try 'everyapi mcp install' to register, 'everyapi mcp uninstall' to remove, or 'everyapi mcp' (no args) to run the server.\n", args[0])
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
