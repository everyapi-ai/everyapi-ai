package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

// probeClient returns one of these state constants. Kept as
// English literals (not translated yet here) so the switch in
// Status can dispatch on a stable identifier; the human-visible
// labels go through i18n.T in Status itself.
const (
	stateNotInstalled  = "not on PATH"
	stateRegistered    = "registered"
	stateNotRegistered = "not registered"
	stateProbeFailed   = "(probe failed)"
)

// Status walks every supported MCP client and reports whether
// everyapi is registered there. Symmetric counterpart to
// Install / Uninstall, useful both interactively (the user
// forgot which clients they've wired up) and as a script gate
// (exit code reflects whether at least one client is registered).
//
// Detection strategy: shell out to `<client> mcp list` and look
// for the substring "everyapi" in the combined output. Each
// client formats its list differently and the schema drifts
// across versions, but the server name is always present
// somewhere. Cheaper and more durable than parsing the on-disk
// config (which would also need three different parsers — JSON
// for claude/gemini, TOML for codex).
func Status(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprint(os.Stdout, i18n.T("mcp.status_usage"))
		return nil
	}

	names := clientNames()
	cliout.Printf(i18n.T("mcp.status.count")+"\n\n", len(names))

	registered := 0
	for _, n := range names {
		c := mcpClients[n]
		state := probeClient(c)
		var (
			label string
			extra = c.ConfigPath
		)
		switch state {
		case stateNotInstalled:
			label = i18n.T("mcp.status.state_not_installed")
			// c.InstallHint already starts with "Install …" so just
			// surface it verbatim instead of prefixing "install:".
			extra = c.InstallHint
		case stateRegistered:
			label = i18n.T("mcp.status.state_registered")
			registered++
		case stateNotRegistered:
			label = i18n.T("mcp.status.state_not_registered")
			extra = fmt.Sprintf(i18n.T("mcp.status.run_install_hint"), n)
		default:
			label = i18n.T("mcp.status.state_probe_failed")
		}
		cliout.Printf("  %-7s %-15s  %s\n", n, label, extra)
	}
	cliout.Printf("\n"+i18n.T("mcp.status.registered_count")+"\n", registered, len(names))
	return nil
}

// probeClient returns one of the state constants above. Pulled out
// for testability — status_test.go drives it with a stub mcpClient
// whose Name points at /bin/echo (always-on-PATH, predictable
// output) and a ListArgv that produces / doesn't produce the
// "everyapi" substring.
//
// Probe is bounded by probeTimeout because a misbehaving client
// binary (claude / codex / gemini have all shipped builds that
// hung on stdin under specific conditions) would otherwise lock
// `everyapi mcp status` and any caller that polls it.
func probeClient(c *mcpClient) string {
	if _, err := exec.LookPath(c.Name); err != nil {
		return "not on PATH"
	}
	if c.ListArgv == nil {
		// Defensive: an entry without a ListArgv can't be probed.
		// Treat as unknown so the rest of the table still renders.
		return "(probe failed)"
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Name, c.ListArgv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// claude / codex / gemini all exit 0 even when zero servers
		// are registered (printed e.g. "No MCP servers configured")
		// — a non-zero exit means the binary itself broke, not that
		// our server is missing. A context-deadline error lands here
		// too; the same "(probe failed)" label is the right outcome.
		return "(probe failed)"
	}
	if strings.Contains(string(out), "everyapi") {
		return "registered"
	}
	return "not registered"
}

// probeTimeout caps each per-client `mcp list` shell-out. 5s is
// long enough that a healthy client on a sleepy laptop finishes,
// short enough that three hung probes (one per client) finish the
// whole `mcp status` command in 15s instead of forever.
const probeTimeout = 5 * time.Second
