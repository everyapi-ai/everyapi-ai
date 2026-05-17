package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Install wires `relaya mcp` into a supported MCP client's config so
// the user doesn't have to hand-edit JSON. v1 supports Claude Code
// only (the only client whose CLI ships a config-mutation
// subcommand we can shell out to); other clients land here when
// they expose a similar interface.
//
// Why shell out instead of editing `~/.claude/settings.json`
// ourselves: Claude Code knows its own config layout (path, schema,
// project-vs-user precedence) better than we do, and that surface
// changes across Claude Code versions. `claude mcp add` is the
// stable API.
func Install(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stdout, installUsage)
		return nil
	}

	// Claude Code's `mcp add` syntax:
	//   claude mcp add <name> <command> [args...]
	// We register the server under the name "relaya", with command =
	// the relaya binary (located via PATH so brew / go-install / etc.
	// all work) and arg = "mcp". After this the user's MCP client
	// can spawn us by name.
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("`claude` CLI not found on PATH. Install Claude Code from https://docs.claude.com/en/docs/claude-code/setup, or hand-edit ~/.claude/settings.json with:\n\n%s", manualSnippet)
	}

	cmd := exec.Command("claude", "mcp", "add", "relaya", "relaya", "mcp")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// `claude mcp add` prints its own error to stderr — we just
		// need to propagate exit code semantics. Wrap so the top-
		// level main.go's "Error: %s" line carries useful context.
		return fmt.Errorf("claude mcp add failed: %w", err)
	}

	fmt.Fprintln(os.Stdout, strings.TrimSpace(`
Registered as `+"`relaya`"+` in Claude Code. Restart Claude Code (or run
`+"`claude mcp list`"+` to verify). Then try asking it
"what's my Relaya balance?" — the AI will invoke relaya_status.
`))
	return nil
}

const installUsage = `relaya mcp install — register relaya as an MCP server with Claude Code

USAGE
  relaya mcp install

Runs ` + "`claude mcp add relaya relaya mcp`" + ` under the hood. After this
your Claude Code spawns ` + "`relaya mcp`" + ` on demand, exposing the four
relaya_* tools (status, topup, seller_list, seller_withdraw).

Requires the ` + "`claude`" + ` CLI on PATH. If Claude Code isn't installed,
hand-edit ~/.claude/settings.json with the snippet printed in the
error message.`

const manualSnippet = `  {
    "mcpServers": {
      "relaya": { "command": "relaya", "args": ["mcp"] }
    }
  }`
