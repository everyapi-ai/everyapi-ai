package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Install wires `everyapi mcp` into a supported MCP client's config so
// the user doesn't have to hand-edit JSON.
//
// Usage:
//
//	everyapi mcp install              # default: claude (back-compat)
//	everyapi mcp install claude
//	everyapi mcp install codex
//	everyapi mcp install gemini
//
// Why shell out instead of editing the client's settings.json
// ourselves: each client owns its own config layout (path, schema,
// scope precedence) and that surface changes across versions. The
// `<client> mcp add` subcommand is the stable API.
func Install(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stdout, installUsage)
		return nil
	}

	clientName := "claude"
	if len(args) > 0 {
		clientName = args[0]
	}

	c, err := lookupClient(clientName)
	if err != nil {
		return err
	}

	if _, err := exec.LookPath(c.Name); err != nil {
		return fmt.Errorf("`%s` CLI not found on PATH. %s\n\nAlternatively, paste the following into %s:\n\n%s", c.Name, c.InstallHint, c.ConfigPath, c.ManualSnippet)
	}

	argv := c.AddArgv("everyapi", "everyapi", []string{"mcp"})
	cmd := exec.Command(c.Name, argv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// The client CLI prints its own error to stderr — we just need
		// to propagate exit-code semantics. Wrap so the top-level
		// main.go's "Error: %s" line carries useful context.
		return fmt.Errorf("%s mcp add failed: %w", c.Name, err)
	}

	fmt.Fprintln(os.Stdout, strings.TrimSpace(`
Registered as `+"`everyapi`"+` in `+c.Name+`. Restart `+c.Name+` (or run
`+"`"+c.Name+` mcp list`+"`"+` to verify). Then try asking it
"what's my EveryAPI balance?" — the AI will invoke everyapi_status.
`))
	return nil
}

const installUsage = `everyapi mcp install — register everyapi as an MCP server with an AI CLI

USAGE
  everyapi mcp install [client]

ARGUMENTS
  client    One of: claude, codex, gemini. Defaults to claude.

Runs ` + "`<client> mcp add everyapi everyapi mcp`" + ` under the hood (with the
syntax each client expects). After this your AI CLI spawns ` + "`everyapi mcp`" + `
on demand, exposing the four everyapi_* tools (status, topup, seller_list,
seller_withdraw).

Requires the chosen client's CLI on PATH. If it isn't installed, the
error message includes the install link and a config snippet you can
paste into the client's settings by hand.`
