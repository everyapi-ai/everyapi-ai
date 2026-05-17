package mcp

import (
	"fmt"
	"os"
	"os/exec"
)

// Uninstall is the inverse of Install: it removes the "relaya" MCP
// server registration from the chosen AI CLI by shelling out to
// `<client> mcp remove relaya`.
//
// Usage:
//
//	relaya mcp uninstall            # default: claude (back-compat)
//	relaya mcp uninstall claude
//	relaya mcp uninstall codex
//	relaya mcp uninstall gemini
func Uninstall(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stdout, uninstallUsage)
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
		return fmt.Errorf("`%s` CLI not found on PATH. If you registered relaya by hand-editing the client's settings, remove the \"relaya\" entry from the mcpServers object manually", c.Name)
	}

	argv := c.RemoveArgv("relaya")
	cmd := exec.Command(c.Name, argv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s mcp remove failed: %w", c.Name, err)
	}

	fmt.Fprintf(os.Stdout, "Unregistered `relaya` from %s. Restart %s to drop the running server.\n", c.Name, c.Name)
	return nil
}

const uninstallUsage = `relaya mcp uninstall — unregister relaya from an AI CLI

USAGE
  relaya mcp uninstall [client]

ARGUMENTS
  client    One of: claude, codex, gemini. Defaults to claude.

Runs ` + "`<client> mcp remove relaya`" + ` under the hood, undoing what
` + "`relaya mcp install [client]`" + ` did. Restart the client afterward to
drop the running server process.

Requires the chosen client's CLI on PATH. If you registered relaya by
hand-editing the client's settings, remove the "relaya" entry from the
mcpServers object manually.`
