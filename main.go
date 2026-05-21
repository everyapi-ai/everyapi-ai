// everyapi — CLI for the EveryAPI AI API gateway.
//
// Covers the V1 buyer onboarding flow (login, logout, status, use)
// AND hosts the MCP server as the `mcp` subcommand — same Go
// module, single binary, one install. See README.md for the full
// command surface and the design rationale.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/cmd"
	"github.com/everyapi-ai/everyapi-ai/cmd/admin"
	"github.com/everyapi-ai/everyapi-ai/cmd/edge"
	mcpcmd "github.com/everyapi-ai/everyapi-ai/cmd/mcp"
	"github.com/everyapi-ai/everyapi-ai/cmd/proxy"
	"github.com/everyapi-ai/everyapi-ai/cmd/seller"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/mcp"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi — EveryAPI CLI

USAGE
  everyapi <command> [flags]

COMMANDS
  login           Authenticate this device with EveryAPI
  logout          Remove this device's credentials
  status          Show current quota, usage, and balance
  topup           Open the wallet top-up page (anti-phishing verification phrase)
  use <tool>      Launch a third-party CLI (claude / codex / gemini) via EveryAPI
  seller <sub>    Channel-marketplace seller commands
__ADMIN_BLOCK__
  edge <sub>      BYO-GPU supplier agent (docker + ollama)
  proxy <sub>     Local sanitizer proxy (privacy filter for SDK requests)
  mcp [sub]       MCP server for AI CLIs (Claude Code / Codex / Gemini)
  update          Check for a newer release and run the matching upgrade
  version         Print the build version
  help            Show this message

Run 'everyapi <command> help' for subcommand details and flags
(e.g. 'everyapi seller help', 'everyapi edge help').
`

// command is a single top-level subcommand. The registry below
// replaces the older switch/case dispatch — adding a command is now
// one line in `commands`, and the help-flag handling stays uniform.
type command struct {
	name string
	// aliases lets `version` also answer to `--version` / `-v` without
	// a special case in main. The principle is "alias = synonym", not
	// "alias = different behavior".
	aliases []string
	// desc is the one-line summary the launcher (bare `everyapi` on
	// a TTY) renders next to the command name. Mirrors the text in
	// the static `usage` block — kept in sync by review, not code:
	// the usage block targets the piped / -h reader and the
	// launcher targets the interactive picker.
	desc string
	// adminOnly hides the row from the launcher when the cached
	// credential isn't an admin user. Mirrors adminUsageBlock's
	// gating in renderUsage. Hides UI affordances the user can't
	// usefully exercise; the backend still 403s if a non-admin
	// invokes the command anyway, so this is cosmetic.
	adminOnly bool
	// subs is the subcommand menu rendered when this command is
	// picked from the launcher (or invoked bare on a TTY without
	// arguments). Each entry's args slice is passed verbatim to
	// run, so a row {args: []string{"marketplace", "on"}} dispatches
	// the same way `everyapi admin marketplace on` would. Only
	// includes subcommands that are useful without further flags —
	// flag-required actions (seller add-key, edge register --name N)
	// stay command-line-only. Empty/nil means "no sub-menu — pick
	// runs the command bare".
	subs []subcommand
	run  func(args []string) error
}

// subcommand is one row in a command group's sub-menu rendered by
// runSubPicker. name is what the picker shows; desc is the help
// blurb to its right; args is what the parent command's run gets
// when this row is selected.
type subcommand struct {
	name string
	desc string
	args []string
}

// commands is the registered set, in the order they appear in the
// help text. main() walks this slice (not a map) so the lookup order
// matches the documented order — keeps the "which command runs when
// two names conflict" question impossible.
var commands = []command{
	{name: "login", desc: "Authenticate this device with EveryAPI", run: cmd.Login},
	{name: "logout", desc: "Remove this device's credentials", run: cmd.Logout},
	{name: "status", desc: "Show current quota, usage, and balance", run: cmd.Status},
	{name: "topup", desc: "Open the wallet top-up page (anti-phishing verification phrase)", run: cmd.Topup},
	{name: "use", desc: "Launch a third-party CLI (claude / codex / gemini) via EveryAPI", run: cmd.Use},
	{name: "seller", desc: "Channel-marketplace seller commands", run: seller.Run, subs: []subcommand{
		{name: "list", desc: "List the channels you've mounted", args: []string{"list"}},
		{name: "setup", desc: "Interactive add-channel wizard (API key or OAuth: codex / claude / gemini)", args: []string{"setup"}},
		{name: "withdraw", desc: "Transfer pending seller earnings to main balance", args: []string{"withdraw"}},
	}},
	{name: "edge", desc: "BYO-GPU supplier agent (docker + ollama)", run: edge.Run, subs: []subcommand{
		{name: "register", desc: "Register this machine as an edge node (prompts for name)", args: []string{"register"}},
		{name: "list", desc: "List nodes on the active backend", args: []string{"list"}},
		{name: "status", desc: "docker compose ps + dashboard view of the active node", args: []string{"status"}},
		{name: "start", desc: "Detect hardware + docker compose up", args: []string{"start"}},
		{name: "stop", desc: "docker compose down", args: []string{"stop"}},
		{name: "logs", desc: "docker compose logs", args: []string{"logs"}},
		{name: "models", desc: "List / pull / remove ollama models on the active node", args: []string{"models"}},
		{name: "update", desc: "docker compose pull && up", args: []string{"update"}},
		{name: "remove", desc: "Remove the active node + delete backend row", args: []string{"remove"}},
	}},
	{name: "admin", desc: "Operator commands (admin role required)", adminOnly: true, run: admin.Run, subs: []subcommand{
		{name: "marketplace status", desc: "Show marketplace.enabled flag", args: []string{"marketplace", "status"}},
		{name: "marketplace on", desc: "Open the marketplace", args: []string{"marketplace", "on"}},
		{name: "marketplace off", desc: "Close the marketplace", args: []string{"marketplace", "off"}},
	}},
	{name: "proxy", desc: "Local sanitizer proxy (privacy filter for SDK requests)", run: proxy.Run, subs: []subcommand{
		{name: "start", desc: "Run the sanitizer proxy (asks background vs foreground)", args: []string{"start"}},
		{name: "stop", desc: "Stop the running proxy (uses PID file)", args: []string{"stop"}},
		{name: "status", desc: "Show running stats", args: []string{"status"}},
		{name: "configure", desc: "Interactive detector + custom-pattern setup", args: []string{"configure"}},
	}},
	{name: "mcp", desc: "MCP server for AI CLIs (Claude Code / Codex / Gemini)", run: runMCP, subs: []subcommand{
		{name: "install", desc: "Auto-register everyapi as an MCP server (default: claude)", args: []string{"install"}},
		{name: "uninstall", desc: "Remove the MCP registration", args: []string{"uninstall"}},
	}},
	{name: "update", desc: "Check for a newer release and run the matching upgrade", run: cmd.Update},
	{name: "version", aliases: []string{"--version", "-v"}, desc: "Print the build version", run: cmd.Version},
}

// adminUsageBlock is appended to the base usage by renderUsage when
// the logged-in user has admin role. Keeping it out of the base
// string means non-admin users don't see commands they can't run —
// the backend still rejects them with a 403 if a non-admin types the
// command anyway, but the help output stays clean.
const adminUsageBlock = `  admin <sub>     Operator commands (admin role required)
`

// adminBlockSentinel is the placeholder inside the static `usage`
// string that renderUsage substitutes. A sentinel rather than a
// text-anchor (e.g. strings.Index(usage, "  proxy <sub>")) is more
// robust to future help-text reorders — the placeholder moves with
// the help block, so reordering the COMMANDS section can't silently
// orphan the admin block at the end.
const adminBlockSentinel = "__ADMIN_BLOCK__\n"

// renderUsage returns the usage block, substituting adminUsageBlock
// into the sentinel iff the cached credential's Role indicates an
// admin user. Non-admin / unauthenticated callers see the sentinel
// stripped (no leak). Falls through to plain strip on any
// credential-load error.
func renderUsage() string {
	creds, err := config.Load()
	if err != nil || !creds.IsAdmin() {
		return strings.Replace(usage, adminBlockSentinel, "", 1)
	}
	return strings.Replace(usage, adminBlockSentinel, adminUsageBlock, 1)
}

// lookup resolves a CLI-typed name (incl. aliases) to its command.
func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
		for _, a := range c.aliases {
			if a == name {
				return c, true
			}
		}
	}
	return command{}, false
}

// runLauncher is the bare-`everyapi` interactive entry point: shows
// every visible command in a huh-backed picker, then dispatches the
// chosen one with no extra args. Each command's own no-arg handler
// takes over from there (e.g. `use` opens its tool picker, `seller`
// renders its subcommand help / picker).
//
// Hidden from non-admin users:
//   - rows marked adminOnly
//   - --version / -v aliases (the canonical "version" row stays)
//
// Esc / Ctrl-C from a NESTED picker (a tool picker, group picker,
// confirm dialog, etc. surfaced by the dispatched command) returns
// here and re-renders the launcher — that's the "back to parent
// level" affordance. Esc / Ctrl-C from the launcher itself exits
// cleanly with status 0.
func runLauncher() error {
	creds, _ := config.Load()
	isAdmin := creds != nil && creds.IsAdmin()

	var visible []command
	var labels []string
	maxName := 0
	for _, c := range commands {
		if c.adminOnly && !isAdmin {
			continue
		}
		if len(c.name) > maxName {
			maxName = len(c.name)
		}
		visible = append(visible, c)
	}
	for _, c := range visible {
		labels = append(labels, fmt.Sprintf("%-*s  %s", maxName, c.name, c.desc))
	}

	lastSel := 0
	for {
		idx, err := cliprompt.PickWithSelected("EveryAPI — pick a command:", labels, lastSel)
		if err != nil {
			if errors.Is(err, cliprompt.ErrPickCancelled) {
				return nil
			}
			return err
		}
		lastSel = idx
		err = dispatchInteractive(visible[idx], nil)
		// Stay in the menu regardless of how the dispatched
		// command returned. Real errors (not-logged-in,
		// transient API failure, etc.) print to stderr and the
		// loop re-renders, so the user can pick 'login' or some
		// other command without re-spawning the CLI. Without
		// this, leaf commands that surface errors (status,
		// topup) eject the user from the launcher and leaf
		// commands that print friendly messages and return nil
		// (proxy status when nothing's running) don't — same
		// menu, two contradictory exit semantics.
		if err != nil &&
			!errors.Is(err, cliprompt.ErrPickCancelled) &&
			!errors.Is(err, io.EOF) {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		}
		cliout.Println("")
	}
}

// dispatchInteractive is the single entry point for "run a command
// the way an interactive user would expect". If the command has a
// subs menu and the user hasn't already typed a subcommand on the
// argv, render the sub-picker. Otherwise dispatch verbatim.
//
// The sub-picker has the same back-on-cancel UX as the launcher:
// Esc returns the sentinel ErrPickCancelled so the CALLER's loop
// (launcher or main) can re-render the parent menu instead of
// exiting the process.
//
// Non-TTY callers (CI / piped) skip the picker — they get the
// command's original "no subcommand specified" usage text exactly
// the way it printed before any of this picker code existed.
func dispatchInteractive(c command, args []string) error {
	if len(args) > 0 || len(c.subs) == 0 || !cliprompt.IsInteractive() {
		return c.run(args)
	}
	return runSubPicker(c)
}

// runSubPicker renders a huh-backed Select over c.subs and
// dispatches the chosen row's args to c.run. Loops on
// ErrPickCancelled from the dispatched action (so cancelling a
// nested prompt re-renders the sub-picker), returns
// ErrPickCancelled itself when the user cancels the sub-picker
// (so the caller — runLauncher or main — re-renders THE PARENT).
func runSubPicker(c command) error {
	maxName := 0
	for _, s := range c.subs {
		if len(s.name) > maxName {
			maxName = len(s.name)
		}
	}
	labels := make([]string, len(c.subs))
	for i, s := range c.subs {
		labels[i] = fmt.Sprintf("%-*s  %s", maxName, s.name, s.desc)
	}
	prompt := fmt.Sprintf("%s — pick a subcommand:", c.name)
	lastSel := 0
	for {
		idx, err := cliprompt.PickWithSelected(prompt, labels, lastSel)
		if err != nil {
			return err
		}
		lastSel = idx
		err = c.run(c.subs[idx].args)
		// Same "stay in the menu" rule as runLauncher: real
		// errors get printed to stderr but don't eject the
		// user from this sub-picker — they can pick something
		// else or Esc to go up. Esc from THIS picker (handled
		// above) bubbles up so the launcher re-renders the
		// parent menu.
		if err != nil &&
			!errors.Is(err, cliprompt.ErrPickCancelled) &&
			!errors.Is(err, io.EOF) {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		}
		cliout.Println("")
	}
}

func main() {
	if len(os.Args) < 2 {
		if cliprompt.IsInteractive() {
			if err := runLauncher(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", err)
				os.Exit(1)
			}
			return
		}
		fmt.Print(renderUsage())
		os.Exit(2)
	}
	name := os.Args[1]
	args := os.Args[2:]

	if name == "help" || name == "--help" || name == "-h" {
		fmt.Print(renderUsage())
		return
	}

	c, ok := lookup(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, renderUsage())
		os.Exit(2)
	}

	err := dispatchInteractive(c, args)
	if err == nil {
		return
	}
	// User cancelled an interactive prompt (Esc / Ctrl-C) inside
	// the dispatched command. Don't treat it as an error worth
	// printing to stderr — fall through to the launcher so the
	// user can pick a different command without a fresh shell
	// invocation. Matches the mental model "Esc = up one level"
	// regardless of how the CLI was entered.
	if cliprompt.IsInteractive() &&
		(errors.Is(err, cliprompt.ErrPickCancelled) || errors.Is(err, io.EOF)) {
		if lerr := runLauncher(); lerr != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", lerr)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "Error: %s\n", err)
	os.Exit(1)
}

// runMCP is the `mcp` family dispatcher. Three shapes:
//
//	everyapi mcp                       → run the JSON-RPC server on stdio
//	everyapi mcp install [client]      → register via `<client> mcp add`
//	everyapi mcp uninstall [client]    → unregister via `<client> mcp remove`
//
// Kept here (not in cmd/mcp) so the server entry point (internal/mcp)
// and the install/uninstall handlers (cmd/mcp) don't have to know
// about each other — main wires both.
func runMCP(args []string) error {
	if len(args) == 0 {
		return mcp.Run(os.Stdin, os.Stdout, os.Stderr)
	}
	switch args[0] {
	case "install":
		return mcpcmd.Install(args[1:])
	case "uninstall":
		return mcpcmd.Uninstall(args[1:])
	case "help", "--help", "-h":
		// `everyapi mcp --help` used to fall through to the unknown-
		// subcommand branch and exit 2 with a confusing message;
		// keep parity with every other command's help-flag handling.
		fmt.Print(renderUsage())
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown 'mcp' subcommand %q. Try 'everyapi mcp install' to register, 'everyapi mcp uninstall' to remove, or 'everyapi mcp' (no args) to run the server.\n", args[0])
		os.Exit(2)
		return nil // unreachable
	}
}
