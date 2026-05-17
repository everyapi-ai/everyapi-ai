// relaya — CLI for the Relaya AI API gateway.
//
// See docs/cli/channel-marketplace.md for the design. This binary
// covers the V1 buyer onboarding flow (login, logout, status, use)
// AND hosts the MCP server as the `mcp` subcommand — same Go
// module, single binary, one install.
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
  mcp             Run the Model Context Protocol server on stdio
  version         Print the build version
  help            Show this message

Run 'relaya <command> --help' for command-specific flags.

MCP server: configure your MCP client (Claude Code, Cursor, …) with
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
	case "mcp":
		// MCP takes no flags; any args are ignored. The server is
		// driven entirely by the JSON-RPC stream on stdin.
		err = mcp.Run(os.Stdin, os.Stdout, os.Stderr)
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
