package mcp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	rootcmd "github.com/everyapi-ai/everyapi-ai/cmd"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
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
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprint(os.Stdout, i18n.T("mcp.install_usage"))
		return nil
	}

	clientName, err := resolveClient(args, "install")
	if err != nil {
		return err
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
	// Capture stderr instead of letting it pass through to the user's
	// terminal: we want to inspect it for the "already exists" sentinel
	// before deciding whether the error is fatal or idempotent. The
	// stderr text is re-emitted at the end if we do treat it as fatal.
	cmd.Stdout = os.Stdout
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Run(); err != nil {
		stderrText := stderrBuf.String()
		// The "already exists" error path: claude / codex / gemini
		// all variations of "MCP server <name> already exists ..."
		// when the registration is already in place. That's not an
		// error from our perspective — the user's goal (have
		// everyapi wired into this client) is already achieved.
		// Treat it as a no-op success and fall through to the
		// "Launch now?" prompt so the user isn't stuck retrying.
		if isAlreadyRegistered(stderrText) {
			fmt.Fprintf(os.Stdout, "Already registered in %s — nothing to do.\n", c.Name)
		} else {
			// Real failure: propagate the captured stderr to the
			// user so they see what went wrong, plus the exit-code
			// context.
			if stderrText != "" {
				fmt.Fprint(os.Stderr, stderrText)
				if !strings.HasSuffix(stderrText, "\n") {
					fmt.Fprintln(os.Stderr)
				}
			}
			return fmt.Errorf("%s mcp add failed: %w", c.Name, err)
		}
	} else {
		fmt.Fprintln(os.Stdout, strings.TrimSpace(`
Registered as `+"`everyapi`"+` in `+c.Name+`. Restart `+c.Name+` (or run
`+"`"+c.Name+` mcp list`+"`"+` to verify). Then try asking it
"what's my EveryAPI balance?" — the AI will invoke everyapi_status.
`))
	}

	// Now that the integration is wired, the natural next step is
	// to actually launch the client through EveryAPI — same call
	// the user would have made with `everyapi use <client>`. Offer
	// it on a TTY, skip off-TTY (CI / scripted install). EOF / Esc
	// is treated as "no, don't launch" rather than a real error so
	// the user can cancel without seeing a "operation failed" line.
	if !cliprompt.IsInteractive() {
		return nil
	}
	fmt.Fprintln(os.Stdout, "")
	launch, err := cliprompt.YesNo(
		bufio.NewReader(os.Stdin),
		fmt.Sprintf("Launch %s via EveryAPI now?", c.Name),
		true,
	)
	if err != nil {
		if errors.Is(err, cliprompt.ErrPickCancelled) || errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if !launch {
		return nil
	}
	// rootcmd.Use execs into the chosen client; this call doesn't
	// return on success — control transfers to the client process.
	return rootcmd.Use([]string{c.Name})
}

// isAlreadyRegistered detects the "this MCP server is already in
// the client's config" error path across the three supported clients.
// Each one phrases the same condition a little differently — we
// match on substrings rather than exact text so a phrasing tweak
// in a client patch release doesn't suddenly break the idempotent
// path here.
//
// Patterns observed in the wild:
//
//	claude:  "MCP server everyapi already exists in local config"
//	codex:   "is already configured"  (anecdotal — keep the broader match)
//	gemini:  "already configured"     (anecdotal — keep the broader match)
//
// The lowercase compare neutralises capitalisation differences across
// client versions.
func isAlreadyRegistered(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "already exists") || strings.Contains(s, "already configured")
}

