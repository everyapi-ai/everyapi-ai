package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
	"github.com/everyapi-ai/everyapi-ai/internal/tools"
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

	// Align the colored state badge to the widest localized state word
	// (display width — the words are CJK in some locales) and the client
	// column to the widest name (ASCII).
	badgeW := 0
	for _, w := range []string{
		i18n.T("mcp.status.state_registered"),
		i18n.T("mcp.status.state_not_registered"),
		i18n.T("mcp.status.state_not_installed"),
		i18n.T("mcp.status.state_probe_failed"),
	} {
		if dw := style.Width(w); dw > badgeW {
			badgeW = dw
		}
	}
	nameW := 0
	for _, n := range names {
		if len(n) > nameW {
			nameW = len(n)
		}
	}

	registered := 0
	for _, n := range names {
		c := mcpClients[n]
		var (
			word  string
			tone  style.Tone
			extra string
		)
		switch probeClient(c) {
		case stateRegistered:
			word, tone, extra = i18n.T("mcp.status.state_registered"), style.ToneGreen, c.ConfigPath
			registered++
		case stateNotRegistered:
			word, tone, extra = i18n.T("mcp.status.state_not_registered"), style.ToneYellow, fmt.Sprintf(i18n.T("mcp.status.run_install_hint"), n)
		case stateNotInstalled:
			// c.InstallHint already starts with "Install …".
			word, tone, extra = i18n.T("mcp.status.state_not_installed"), style.ToneGray, c.InstallHint
		default:
			word, tone, extra = i18n.T("mcp.status.state_probe_failed"), style.ToneRed, ""
		}
		// Pad the word to badgeW (display width) inside a 1-space margin
		// so every chip is the same size.
		chip := style.Badge(" "+word+strings.Repeat(" ", badgeW-style.Width(word))+" ", tone)
		cliout.Printf("  %s  %-*s  %s\n", chip, nameW, n, extra)
	}
	cliout.Printf("\n"+i18n.T("mcp.status.registered_count")+"\n", registered, len(names))
	// Honor the documented script-gate contract: a non-zero exit when
	// no client has everyapi registered (main maps a returned error to
	// os.Exit(1)). The table above already printed the human-readable
	// detail; this only affects the exit code.
	if registered == 0 {
		return errors.New(i18n.T("mcp.status.none_registered"))
	}
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
	// Same resolution `use` applies: a client in an off-$PATH npm
	// global dir is installed, not "not installed" — and probing it
	// needs its dir on the child PATH for the co-located node.
	path, env, ok := tools.LookupExecName(c.Name)
	if !ok {
		return stateNotInstalled
	}
	if c.ListArgv == nil {
		// Defensive: an entry without a ListArgv can't be probed.
		// Treat as unknown so the rest of the table still renders.
		return stateProbeFailed
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, c.ListArgv()...)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		// claude / codex / gemini all exit 0 even when zero servers
		// are registered (printed e.g. "No MCP servers configured")
		// — a non-zero exit means the binary itself broke, not that
		// our server is missing. A context-deadline error lands here
		// too; the same "probe failed" label is the right outcome.
		return stateProbeFailed
	}
	if strings.Contains(string(out), "everyapi") {
		return stateRegistered
	}
	return stateNotRegistered
}

// probeTimeout caps each per-client `mcp list` shell-out. 5s is
// long enough that a healthy client on a sleepy laptop finishes,
// short enough that three hung probes (one per client) finish the
// whole `mcp status` command in 15s instead of forever.
const probeTimeout = 5 * time.Second
