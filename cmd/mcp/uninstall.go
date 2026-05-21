package mcp

import (
	"fmt"
	"os"
	"os/exec"
)

// Uninstall is the inverse of Install: it removes the "everyapi" MCP
// server registration from the chosen AI CLI by shelling out to
// `<client> mcp remove everyapi`.
//
// Usage:
//
//	everyapi mcp uninstall            # default: claude (back-compat)
//	everyapi mcp uninstall claude
//	everyapi mcp uninstall codex
//	everyapi mcp uninstall gemini
func Uninstall(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprintln(os.Stdout, uninstallUsage)
		return nil
	}

	clientName, err := resolveClient(args, "uninstall")
	if err != nil {
		return err
	}
	c, err := lookupClient(clientName)
	if err != nil {
		return err
	}

	if _, err := exec.LookPath(c.Name); err != nil {
		return fmt.Errorf("`%s` CLI not found on PATH. If you registered everyapi by hand-editing the client's settings, remove the \"everyapi\" entry from the mcpServers object manually", c.Name)
	}

	argv := c.RemoveArgv("everyapi")
	cmd := exec.Command(c.Name, argv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s mcp remove failed: %w", c.Name, err)
	}

	fmt.Fprintf(os.Stdout, "Unregistered `everyapi` from %s. Restart %s to drop the running server.\n", c.Name, c.Name)
	return nil
}

const uninstallUsage = `everyapi mcp uninstall — unregister everyapi from an AI CLI

USAGE
  everyapi mcp uninstall [client]

ARGUMENTS
  client    One of: claude, codex, gemini. Defaults to claude.

Runs ` + "`<client> mcp remove everyapi`" + ` under the hood, undoing what
` + "`everyapi mcp install [client]`" + ` did. Restart the client afterward to
drop the running server process.

Requires the chosen client's CLI on PATH. If you registered everyapi by
hand-editing the client's settings, remove the "everyapi" entry from the
mcpServers object manually.`
