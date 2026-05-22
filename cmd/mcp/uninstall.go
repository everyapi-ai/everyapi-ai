package mcp

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
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
		fmt.Fprint(os.Stdout, i18n.T("mcp.uninstall_usage"))
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

