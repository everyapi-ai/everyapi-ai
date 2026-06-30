package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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
	// Capture stderr (like Install) so we can recognise the "not
	// registered" path and treat it as an idempotent no-op success rather
	// than a failure: a second `mcp uninstall`, or uninstalling a client
	// where everyapi was never wired, has already achieved the user's goal
	// (everyapi not registered here). Captured stderr is re-emitted on both
	// the success and fatal-error paths below; only the not-registered case
	// suppresses it.
	cmd.Stdout = os.Stdout
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Run(); err != nil {
		stderrText := stderrBuf.String()
		if isNotRegistered(stderrText) {
			fmt.Fprintf(os.Stdout, "`everyapi` was not registered in %s — nothing to do.\n", c.Name)
			return nil
		}
		if stderrText != "" {
			fmt.Fprint(os.Stderr, stderrText)
			if !strings.HasSuffix(stderrText, "\n") {
				fmt.Fprintln(os.Stderr)
			}
		}
		return fmt.Errorf("%s mcp remove failed: %w", c.Name, err)
	}

	// Surface any stderr the remove wrote on the SUCCESS path too (warnings,
	// deprecation / next-step notes). Capturing it for the not-registered
	// check above must not silently swallow it when the remove succeeds.
	if stderrText := stderrBuf.String(); stderrText != "" {
		fmt.Fprint(os.Stderr, stderrText)
		if !strings.HasSuffix(stderrText, "\n") {
			fmt.Fprintln(os.Stderr)
		}
	}

	fmt.Fprintf(os.Stdout, "Unregistered `everyapi` from %s. Restart %s to drop the running server.\n", c.Name, c.Name)
	return nil
}

// isNotRegistered detects the "everyapi isn't in this client's config"
// error path across the supported clients, so `mcp uninstall` is
// idempotent — the inverse of install.go's isAlreadyRegistered. We match
// on substrings rather than exact text so a phrasing tweak in a client
// patch release doesn't break the idempotent path. Patterns observed:
//
//	claude:  "No MCP server found with name: everyapi"
//	codex:   "not found" / "is not configured"  (anecdotal — broader match)
//	gemini:  "not found" / "not configured"     (anecdotal — broader match)
func isNotRegistered(stderr string) bool {
	s := strings.ToLower(stderr)
	// Require the server name to appear alongside a not-registered phrase, so
	// an unrelated failure that merely mentions "not found" / "not configured"
	// (e.g. a "profile not found", a config parse error, a missing config
	// path) is NOT swallowed as a no-op success — that would leave everyapi
	// registered while reporting it removed. The remove command is
	// `<client> mcp remove everyapi`, so a genuine "everyapi isn't registered"
	// error names it; if it doesn't, we fall through to the real error.
	if !strings.Contains(s, "everyapi") {
		return false
	}
	return strings.Contains(s, "no mcp server found") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "not configured")
}

