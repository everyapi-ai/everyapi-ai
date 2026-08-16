package mcp

import (
	"fmt"
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

// Uninstall is the inverse of Install: it removes the "everyapi" MCP server registration from the chosen AI CLI by shelling out to `<client> mcp remove everyapi`.
//
// Usage:
//
//	everyapi mcp uninstall            # default: claude (back-compat) everyapi mcp uninstall claude everyapi mcp uninstall codex everyapi mcp uninstall gemini
func Uninstall(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprint(os.Stdout, i18n.T("mcp.uninstall_usage"))
		return nil
	}
	if len(args) > 1 {
		return cliargs.RequireExact(args, 1)
	}

	clientName, err := resolveClient(args, "uninstall")
	if err != nil {
		return err
	}
	c, err := lookupClient(clientName)
	if err != nil {
		return err
	}

	// ManualOnly targets: install printed a block for the user to paste, so uninstall tells them what to delete. Symmetry is the point — driving deletion of a config we refused to write would be incoherent in the dangerous direction. `librefang mcp remove everyapi` does exist, but its "not configured" error is localized and goes to stdout, so isNotRegistered could not classify it and a second uninstall would hard-fail for every non-English user.
	if c.ManualOnly {
		fmt.Fprint(os.Stdout, manualUninstallText(c))
		return nil
	}

	// Same resolution `use` applies — a client sitting in an off-$PATH npm global dir counts as installed here too.
	if _, _, ok := tools.LookupExecName(c.Name); !ok {
		return fmt.Errorf("`%s` CLI not found on PATH. If you registered everyapi by hand-editing the client's settings, remove the \"everyapi\" entry from the mcpServers object manually", c.Name)
	}

	argv := c.RemoveArgv("everyapi")
	cmd := clientCmd(c.Name, argv)
	// Capture stderr (like Install) so we can recognise the "not registered" path and treat it as an idempotent no-op success rather than a failure: a second `mcp uninstall`, or uninstalling a client where everyapi was never wired, has already achieved the user's goal (everyapi not registered here). Captured stderr is re-emitted on both the success and fatal-error paths below; only the not-registered case suppresses it.
	var stdoutBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Run(); err != nil {
		stdoutText := stdoutBuf.String()
		stderrText := stderrBuf.String()
		if isNotRegistered(stdoutText + "\n" + stderrText) {
			fmt.Fprintf(os.Stdout, "`everyapi` was not registered in %s — nothing to do.\n", c.Name)
			return nil
		}
		if stdoutText != "" {
			fmt.Fprint(os.Stdout, stdoutText)
			if !strings.HasSuffix(stdoutText, "\n") {
				fmt.Fprintln(os.Stdout)
			}
		}
		if stderrText != "" {
			fmt.Fprint(os.Stderr, stderrText)
			if !strings.HasSuffix(stderrText, "\n") {
				fmt.Fprintln(os.Stderr)
			}
		}
		return fmt.Errorf("%s mcp remove failed: %w", c.Name, err)
	}

	// Gemini 0.50 exits 0 even when the named server is absent and reports the condition only on stderr. Classify that before announcing a removal; otherwise a second idempotent uninstall falsely says it removed something.
	stdoutText := stdoutBuf.String()
	stderrText := stderrBuf.String()
	if isNotRegistered(stdoutText + "\n" + stderrText) {
		fmt.Fprintf(os.Stdout, "`everyapi` was not registered in %s — nothing to do.\n", c.Name)
		return nil
	}

	// Surface all other client output on the success path (normal confirmation, warnings, deprecation / next-step notes).
	if stdoutText != "" {
		fmt.Fprint(os.Stdout, stdoutText)
		if !strings.HasSuffix(stdoutText, "\n") {
			fmt.Fprintln(os.Stdout)
		}
	}
	if stderrText != "" {
		fmt.Fprint(os.Stderr, stderrText)
		if !strings.HasSuffix(stderrText, "\n") {
			fmt.Fprintln(os.Stderr)
		}
	}

	fmt.Fprintf(os.Stdout, "Unregistered `everyapi` from %s. Restart %s to drop the running server.\n", c.Name, c.Name)
	return nil
}

// manualUninstallText is the inverse of manualInstallText: it names what to delete rather than what to paste. Mentions the per-agent allowlist because install told the user to edit it — leaving a stale "everyapi" entry there is harmless but confusing.
//
// Unlike install, the target does have a working remove verb: `librefang mcp remove everyapi` matches a hand-pasted entry by name (it resolves on template_id OR name) and hot-reloads a running daemon. We surface it as an option rather than shelling out ourselves, for two different reasons. We can't run it: its "not configured" error is localized and printed to stdout, so a second uninstall would hard-fail instead of reporting an idempotent no-op. And the user should know before running it that it rewrites config.toml through a TOML round-trip, which drops comments — the same reason install refuses to write the file.
func manualUninstallText(c *mcpClient) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s registrations are hand-edited, so nothing was removed for you.\n\n", c.Name)
	fmt.Fprintf(&b, "Delete the [[mcp_servers]] block whose name = \"everyapi\" from %s,\n", clientConfigPath(c))
	fmt.Fprintf(&b, "drop \"everyapi\" from any agent's mcp_servers allowlist, and restart the daemon.\n\n")
	fmt.Fprintf(&b, "%s can also do the first step for you, and hot-reloads a running daemon:\n\n", c.Name)
	fmt.Fprintf(&b, "  %s mcp remove everyapi\n\n", c.Name)
	fmt.Fprintf(&b, "Note that it rewrites %s and drops any comments in that\n", clientConfigPath(c))
	fmt.Fprintf(&b, "file. Delete the block by hand if you keep comments there.\n")
	return b.String()
}

// isNotRegistered detects the "everyapi isn't in this client's config" error path across the supported clients, so `mcp uninstall` is idempotent — the inverse of install.go's isAlreadyRegistered. We match on substrings rather than exact text so a phrasing tweak in a client patch release doesn't break the idempotent path. Patterns observed:
//
//	claude:  "No MCP server found with name: everyapi" codex:   "not found" / "is not configured"  (anecdotal — broader match) gemini:  "not found" / "not configured"     (anecdotal — broader match)
func isNotRegistered(output string) bool {
	s := strings.ToLower(output)
	// Require the server name to appear alongside a not-registered phrase, so an unrelated failure that merely mentions "not found" / "not configured" (e.g. a "profile not found", a config parse error, a missing config path) is NOT swallowed as a no-op success — that would leave everyapi registered while reporting it removed. The remove command is `<client> mcp remove everyapi`, so a genuine "everyapi isn't registered" error names it; if it doesn't, we fall through to the real error.
	if !strings.Contains(s, "everyapi") {
		return false
	}
	return strings.Contains(s, "no mcp server named") ||
		strings.Contains(s, "no mcp server found with name") ||
		strings.Contains(s, `server "everyapi" not found`) ||
		strings.Contains(s, "server 'everyapi' not found") ||
		strings.Contains(s, `"everyapi" is not configured`) ||
		strings.Contains(s, "'everyapi' is not configured")
}
