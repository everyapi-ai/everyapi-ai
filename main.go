// everyapi — CLI for the EveryAPI AI API gateway.
//
// Covers the V1 buyer onboarding flow (login, logout, status, use)
// AND hosts the MCP server as the `mcp` subcommand — same Go
// module, single binary, one install. See README.md for the full
// command surface and the design rationale.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/cmd"
	"github.com/everyapi-ai/everyapi-ai/cmd/admin"
	"github.com/everyapi-ai/everyapi-ai/cmd/checkin"
	"github.com/everyapi-ai/everyapi-ai/cmd/demand"
	"github.com/everyapi-ai/everyapi-ai/cmd/dispute"
	"github.com/everyapi-ai/everyapi-ai/cmd/dm"
	"github.com/everyapi-ai/everyapi-ai/cmd/doctor"
	"github.com/everyapi-ai/everyapi-ai/cmd/edge"
	"github.com/everyapi-ai/everyapi-ai/cmd/events"
	logcmd "github.com/everyapi-ai/everyapi-ai/cmd/log"
	mcpcmd "github.com/everyapi-ai/everyapi-ai/cmd/mcp"
	"github.com/everyapi-ai/everyapi-ai/cmd/models"
	"github.com/everyapi-ai/everyapi-ai/cmd/notify"
	"github.com/everyapi-ai/everyapi-ai/cmd/perf"
	"github.com/everyapi-ai/everyapi-ai/cmd/proxy"
	"github.com/everyapi-ai/everyapi-ai/cmd/report"
	"github.com/everyapi-ai/everyapi-ai/cmd/seller"
	"github.com/everyapi-ai/everyapi-ai/cmd/settings"
	"github.com/everyapi-ai/everyapi-ai/cmd/subscription"
	"github.com/everyapi-ai/everyapi-ai/cmd/token"
	"github.com/everyapi-ai/everyapi-ai/cmd/upstream"
	usagecmd "github.com/everyapi-ai/everyapi-ai/cmd/usage"
	usercmd "github.com/everyapi-ai/everyapi-ai/cmd/user"
	"github.com/everyapi-ai/everyapi-ai/cmd/wallet"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/mcp"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

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
	//
	// IMPORTANT: this is the FALLBACK string for commandDesc(). The
	// authoritative copy for the launcher picker lives in i18n under
	// `launcher.desc.<name>` (en.toml mirrors this field; other
	// locales translate). If you edit `desc` here, edit `en.toml`'s
	// entry too — otherwise English users see the i18n value (stale)
	// while non-English locales unaffected. The locale-parity test
	// will not catch value drift, only key drift.
	desc string
	// adminOnly hides the row from the launcher when the cached
	// credential isn't an admin user. Mirrors the admin block's
	// gating in renderUsage. Hides UI affordances the user can't
	// usefully exercise; the backend still 403s if a non-admin
	// invokes the command anyway, so this is cosmetic.
	adminOnly bool
	// requireLogin hides the row from the launcher when no
	// credentials are cached. Pure UX: typing the command at the
	// shell still works (and errors with "not logged in"), but the
	// menu shouldn't advertise actions that immediately fail.
	requireLogin bool
	// hideLoggedIn is the inverse — hides the row once credentials
	// exist. Used for the `login` row so the launcher shows EITHER
	// login OR logout, not both at once.
	hideLoggedIn bool
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

// mcpSubs is the picker menu for `everyapi mcp`. Extracted into its
// own var so `runMCP` can hand it to runSubPicker when invoked bare
// on a TTY without referencing the `commands` slice — that ref
// would close a commands → runMCP → lookup → commands package-init
// cycle and refuse to compile.
var mcpSubs = []subcommand{
	{name: "install", desc: "Auto-register everyapi as an MCP server (default: claude)", args: []string{"install"}},
	{name: "uninstall", desc: "Remove the MCP registration", args: []string{"uninstall"}},
	{name: "status", desc: "Show which MCP clients have everyapi registered", args: []string{"status"}},
}

// commands is the registered set, in the order they appear in the
// help text. main() walks this slice (not a map) so the lookup order
// matches the documented order — keeps the "which command runs when
// two names conflict" question impossible.
var commands = []command{
	{name: "login", desc: "Authenticate this device with EveryAPI", hideLoggedIn: true, run: cmd.Login},
	{name: "logout", desc: "Remove this device's credentials", requireLogin: true, run: cmd.Logout},
	{name: "status", desc: "Show current quota, usage, and balance", requireLogin: true, run: cmd.Status},
	{name: "topup", desc: "Open the wallet top-up page (anti-phishing verification phrase)", requireLogin: true, run: cmd.Topup},
	{name: "wallet", desc: "Payment history / methods / redemption keys", requireLogin: true, run: wallet.Run, subs: []subcommand{
		{name: "history", desc: "Paginated payment history", args: []string{"history"}},
		{name: "info", desc: "Enabled payment methods + suggested amounts", args: []string{"info"}},
	}},
	{name: "checkin", desc: "Claim today's daily-grant quota", requireLogin: true, run: checkin.Run, subs: []subcommand{
		{name: "claim", desc: "Claim today's reward", args: []string{"claim"}},
		{name: "status", desc: "Show this month's check-in calendar", args: []string{"status"}},
	}},
	{name: "user", desc: "Profile / 2FA / passkey / oauth bindings / aff code", requireLogin: true, run: usercmd.Run, subs: []subcommand{
		{name: "info", desc: "Rolled-up profile + security view", args: []string{"info"}},
		{name: "2fa", desc: "2FA status", args: []string{"2fa"}},
		{name: "aff", desc: "Show affiliate code", args: []string{"aff"}},
	}},
	{name: "subscription", desc: "Subscription plans / self / billing preference", requireLogin: true, run: subscription.Run, subs: []subcommand{
		{name: "plans", desc: "List enabled subscription plans", args: []string{"plans"}},
		{name: "self", desc: "Show your subscriptions", args: []string{"self"}},
	}},
	{name: "use", desc: "Launch a third-party CLI (claude / codex / gemini) via EveryAPI", requireLogin: true, run: cmd.Use},
	{name: "token", desc: "Manage relay API tokens (list / create / key / revoke / …)", requireLogin: true, run: token.Run, subs: []subcommand{
		// Only flag-free verbs surface in the launcher picker; the
		// rest (create/update/key/revoke/enable/disable/show) need a
		// flag or an id and stay command-line-only.
		{name: "list", desc: "List your tokens (masked keys)", args: []string{"list"}},
	}},
	{name: "log", desc: "Request log: list / stat / summary", requireLogin: true, run: logcmd.Run, subs: []subcommand{
		{name: "list", desc: "Recent log entries (newest first)", args: []string{"list"}},
		{name: "stat", desc: "Quota / RPM / TPM totals for the window", args: []string{"stat"}},
		{name: "summary", desc: "Per-model spend over the last 7d", args: []string{"summary"}},
	}},
	{name: "usage", desc: "Day-by-day quota usage", requireLogin: true, run: usagecmd.Run},
	{name: "models", desc: "Model catalog: list / pricing / groups", requireLogin: true, run: models.Run, subs: []subcommand{
		{name: "list", desc: "Print every model id your group can route to", args: []string{"list"}},
		{name: "pricing", desc: "Per-model rate sheet", args: []string{"pricing"}},
		{name: "groups", desc: "Routing groups your account can use", args: []string{"groups"}},
	}},
	{name: "upstream", desc: "Upstream provider health (status-page rollup)", run: upstream.Run},
	{name: "perf", desc: "Per-model performance (success rate / latency / throughput)", run: perf.Run},
	{name: "demand", desc: "Buyer-side marketplace postings (list / my / show / submit / cancel / remove)", requireLogin: true, run: demand.Run, subs: []subcommand{
		{name: "list", desc: "Public marketplace feed", args: []string{"list"}},
		{name: "my", desc: "Demands you've posted", args: []string{"my"}},
	}},
	{name: "dispute", desc: "Open / list / inspect disputes", requireLogin: true, run: dispute.Run, subs: []subcommand{
		{name: "my", desc: "List your open + resolved disputes", args: []string{"my"}},
	}},
	{name: "report", desc: "File an abuse / TOS-violation report", run: report.Run},
	{name: "notify", desc: "In-app notifications (list / count / read / readall)", requireLogin: true, run: notify.Run, subs: []subcommand{
		{name: "list", desc: "Recent notifications", args: []string{"list"}},
		{name: "count", desc: "Just the unread count", args: []string{"count"}},
		{name: "readall", desc: "Flip every unread to read", args: []string{"readall"}},
	}},
	{name: "dm", desc: "Direct messages (threads / open / send / messages / read)", requireLogin: true, run: dm.Run, subs: []subcommand{
		{name: "threads", desc: "Your DM threads", args: []string{"threads"}},
		{name: "contacts", desc: "Users you've messaged", args: []string{"contacts"}},
		{name: "count", desc: "Unread DM count", args: []string{"count"}},
	}},
	{name: "seller", desc: "Channel-marketplace seller commands", requireLogin: true, run: seller.Run, subs: []subcommand{
		{name: "list", desc: "List the channels you've mounted", args: []string{"list"}},
		{name: "setup", desc: "Interactive add-channel wizard (API key or OAuth: codex / claude / gemini)", args: []string{"setup"}},
		{name: "withdraw", desc: "Transfer pending seller earnings to main balance", args: []string{"withdraw"}},
	}},
	{name: "edge", desc: "BYO-GPU supplier agent (docker + ollama)", requireLogin: true, run: edge.Run, subs: []subcommand{
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
	{name: "admin", desc: "Operator commands (admin role required)", adminOnly: true, requireLogin: true, run: admin.Run, subs: []subcommand{
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
	{name: "mcp", desc: "MCP server for AI CLIs (Claude Code / Codex / Gemini)", run: runMCP, subs: mcpSubs},
	{name: "doctor", desc: "Self-check (creds, gateway, sanitizer, tools)", run: doctor.Run},
	{name: "events", desc: "Subscribe to the live event stream (SSE)", requireLogin: true, run: events.Run},
	{name: "settings", desc: "View / change CLI preferences (language, …)", run: settings.Run},
	{name: "update", desc: "Check for a newer release and run the matching upgrade", run: cmd.Update},
	{name: "uninstall", desc: "Remove everyapi state and binary from this machine", run: cmd.Uninstall},
	{name: "version", aliases: []string{"--version", "-v"}, desc: "Print the build version", run: cmd.Version},
}

// adminBlockSentinel is the placeholder inside the launcher usage
// string that renderUsage substitutes. A sentinel rather than a
// text-anchor (e.g. strings.Index(usage, "  proxy <sub>")) is more
// robust to future help-text reorders — the placeholder moves with
// the help block, so reordering the COMMANDS section can't silently
// orphan the admin block at the end.
const adminBlockSentinel = "__ADMIN_BLOCK__\n"

// renderUsage returns the usage block, substituting the admin block
// into the sentinel iff the cached credential's Role indicates an
// admin user. Non-admin / unauthenticated callers see the sentinel
// stripped (no leak). Falls through to plain strip on any
// credential-load error.
func renderUsage() string {
	base := i18n.T("launcher.usage")
	adminBlock := i18n.T("launcher.usage_admin_block")
	creds, err := config.Load()
	if err != nil || !creds.IsAdmin() {
		return strings.Replace(base, adminBlockSentinel, "", 1)
	}
	return strings.Replace(base, adminBlockSentinel, adminBlock, 1)
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
// resolveLanguage publishes the user's preferred language to both
// the in-process i18n table and EVERYAPI_LANG so SDK calls attach
// Accept-Language. See the doc on main() for the precedence chain.
func resolveLanguage() {
	lang := ""
	if s, err := config.LoadSettings(); err == nil && s != nil {
		lang = s.Language
	}
	if lang == "" {
		lang = i18n.DetectFromEnv()
	}
	i18n.SetLanguage(lang)
	// DetectFromEnv always returns at least LangEn, so lang is
	// guaranteed non-empty here — set the env unconditionally so
	// the SDK's per-request Accept-Language header has a value.
	_ = os.Setenv("EVERYAPI_LANG", lang)
}

// Esc / Ctrl-C from a NESTED picker (a tool picker, group picker,
// confirm dialog, etc. surfaced by the dispatched command) returns
// here and re-renders the launcher — that's the "back to parent
// level" affordance. Esc / Ctrl-C from the launcher itself exits
// cleanly with status 0.
//
// The menu is rebuilt every loop iteration from the current
// credentials, so logging in / out from inside the launcher
// refreshes the visible rows without re-spawning the process. On
// first render an entry probe (sessionRejected) confirms the cached
// token still authenticates — a revoked / expired token drops the
// menu to its logged-out shape instead of advertising commands that
// would only 401.
func runLauncher() error {
	// sessionDead latches once the backend definitively rejects the
	// cached token (the entry probe below). The credentials file is
	// left intact — `login` overwrites it — but for the rest of this
	// launcher session we render the logged-out menu so the user
	// isn't stuck picking commands that all fail with 401.
	sessionDead := false
	probed := false
	lastSel := 0
	for {
		creds, _ := config.Load()
		loggedIn := creds != nil && !sessionDead

		// Entry probe: once, on the first render, only when the
		// cached credentials still claim a live session. A definitive
		// 401 latches sessionDead so the menu drops to logged-out; a
		// network error / 5xx is deliberately NOT a verdict (see
		// sessionRejected) so an offline launch keeps the cached menu.
		if loggedIn && !probed {
			probed = true
			if sessionRejected(creds) {
				sessionDead = true
				loggedIn = false
				fmt.Fprintln(os.Stderr, i18n.T("auth.session_expired"))
			}
		}
		isAdmin := loggedIn && creds.IsAdmin()

		visible, labels := launcherRows(loggedIn, isAdmin)
		// The row set shrinks when the probe (or a logout) flips the
		// menu to logged-out; clamp the remembered cursor so it can't
		// index past the rebuilt slice.
		if lastSel >= len(labels) {
			lastSel = 0
		}

		idx, err := cliprompt.PickWithSelected(i18n.T("launcher.welcome"), labels, lastSel)
		if err != nil {
			if errors.Is(err, cliprompt.ErrPickCancelled) {
				return nil
			}
			return err
		}
		lastSel = idx
		chosen := visible[idx]
		err = dispatchInteractive(chosen, nil)
		// A successful `login` clears the stale-session latch so the
		// next iteration's rebuild shows the logged-in menu instead
		// of staying stuck on the logged-out set.
		if chosen.name == "login" && err == nil {
			sessionDead = false
		}
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
			fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), err)
		}
		cliout.Println("")
	}
}

// nameCell right-pads name to width w with PLAIN spaces, then bolds
// only the name text. Padding stays outside the bold span so the ANSI
// bytes never enter the %-*s-style width math — column alignment holds
// in the picker. Command names are ASCII, so len == display width.
func nameCell(name string, w int) string {
	pad := ""
	if w > len(name) {
		pad = strings.Repeat(" ", w-len(name))
	}
	return style.Bold(name) + pad
}

// launcherRows builds the visible command set and their aligned
// display labels for the given auth state. Split out of runLauncher
// so the loop can rebuild the menu each iteration — the row set
// changes when the user logs in / out or when the entry probe finds
// a stale token.
func launcherRows(loggedIn, isAdmin bool) ([]command, []string) {
	var visible []command
	maxName := 0
	for _, c := range commands {
		if c.adminOnly && !isAdmin {
			continue
		}
		if c.requireLogin && !loggedIn {
			continue
		}
		if c.hideLoggedIn && loggedIn {
			continue
		}
		if len(c.name) > maxName {
			maxName = len(c.name)
		}
		visible = append(visible, c)
	}
	labels := make([]string, len(visible))
	for i, c := range visible {
		labels[i] = nameCell(c.name, maxName) + "  " + commandDesc(c)
	}
	return visible, labels
}

// commandDesc resolves the launcher row's description. Looks up
// `launcher.desc.<name>` in the current locale's table; falls back to
// the hardcoded English `c.desc` when the key is missing (i18n.T
// returns the bare key in that case — using it directly would print
// "launcher.desc.login" to the user). The struct field stays the
// source of truth for the English copy; this helper just plumbs the
// translated variant through when one exists.
func commandDesc(c command) string {
	key := "launcher.desc." + c.name
	if v := i18n.T(key); v != key {
		return style.Emph(v)
	}
	return style.Emph(c.desc)
}

// subcommandDesc is the sub-picker equivalent of commandDesc. The
// key is the PARENT's name + the sub-row's args joined with
// underscores (so admin's "marketplace status" becomes
// `launcher.subs.admin.marketplace_status`), because the sub `name`
// field is a display string that may contain spaces — args is the
// stable identifier.
//
// The `slug == ""` branch is defensive: every current subcommand in
// the registry sets a non-empty args slice, but a future entry that
// only wires up a `run` function with no args (e.g. a top-level
// shortcut row) would otherwise generate a `launcher.subs.X.` key
// with a trailing dot. Falling back to a space-stripped name keeps
// the resulting key human-readable.
func subcommandDesc(parent string, s subcommand) string {
	slug := strings.Join(s.args, "_")
	if slug == "" {
		slug = strings.ReplaceAll(s.name, " ", "_")
	}
	key := "launcher.subs." + parent + "." + slug
	if v := i18n.T(key); v != key {
		return style.Emph(v)
	}
	return style.Emph(s.desc)
}

// launcherProbeTimeout caps the entry-probe round-trip. The SDK's
// http.Client.Timeout is 30s — far too long to stall a menu render
// behind. 3s clears a healthy request yet keeps an offline launch
// responsive.
const launcherProbeTimeout = 3 * time.Second

// sessionRejected reports whether the backend DEFINITIVELY rejects
// the cached credentials — GET /api/user/self answering HTTP 401.
// Every other outcome (timeout, DNS failure, 5xx, success) returns
// false: "couldn't verify" must never masquerade as "logged out",
// or a transient blip walls the user behind a `login` that itself
// needs the network.
func sessionRejected(creds *config.Credentials) bool {
	// Legacy credentials predate the user_id field. Without it the
	// request omits the EveryAPI-User-Id header and UserAuth returns
	// 401 "user ID not provided" — indistinguishable here from a bad
	// token. Skip the probe: a stale logged-in menu for a pre-user_id
	// credential beats falsely walling the user out.
	if creds == nil || creds.UserID <= 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), launcherProbeTimeout)
	defer cancel()
	_, err := api.New(creds.APIBase, creds.AccessToken).
		WithUserID(creds.UserID).
		GetSelf(ctx)
	return api.IsUnauthorized(err)
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
		labels[i] = nameCell(s.name, maxName) + "  " + subcommandDesc(c.name, s)
	}
	prompt := fmt.Sprintf(i18n.T("common.pick_subcommand"), c.name)
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
			fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), err)
		}
		cliout.Println("")
	}
}

func main() {
	// Resolve the user's language preference once at startup and
	// publish it two ways: i18n.SetLanguage for CLI-originated
	// strings, EVERYAPI_LANG env for the SDK to attach as
	// Accept-Language on every API call (so backend errors come
	// back translated). Resolution chain (first-wins):
	//   1. settings.json's `language` field
	//   2. EVERYAPI_LANG / LC_ALL / LC_MESSAGES / LANG env
	//   3. "en"
	// A broken / missing settings file falls through to the env
	// chain rather than failing startup — the user shouldn't be
	// locked out of `everyapi login` because the preference file
	// is corrupt.
	resolveLanguage()

	if len(os.Args) < 2 {
		if cliprompt.IsInteractive() {
			if err := runLauncher(); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), err)
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

	// Auto-update check: cached-only, interactive-only. If a newer
	// release is cached and the user picks "update now", we run the
	// upgrade and skip the original command (an old binary running
	// right after a brew upgrade kicked off is asking for trouble).
	// "Later" / "skip this version" / non-TTY / dev build / opt-out
	// env all fall through to the original command unchanged.
	if cmd.MaybePromptUpdate(name) {
		return
	}

	err := dispatchInteractive(c, args)
	if err == nil {
		return
	}
	// flag.ErrHelp bubbles up when any flag.FlagSet down the call
	// tree sees --help / -h. The FlagSet has already printed its
	// usage to stderr; exit cleanly rather than dress "flag: help
	// requested" up as an Error: line. Makes
	// `everyapi <cmd> [<sub>...] --help` reach the user as help
	// text instead of as a noisy failure at every level.
	if errors.Is(err, flag.ErrHelp) {
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
			fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), lerr)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), err)
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
		// Two callers reach this branch:
		//   (a) An MCP client (Claude Desktop, codex, gemini) that
		//       spawned us through a pipe and wants to speak JSON-RPC
		//       over stdio. stdin is NOT a TTY in this case.
		//   (b) A human who typed `everyapi mcp` at a shell prompt —
		//       both stdin and stdout are TTYs. Used to silently
		//       block on stdin.Read() forever because the server was
		//       waiting for an LSP-style request; looked indistinguish-
		//       able from a hang.
		//
		// Differentiate via cliprompt.IsInteractive (both stdin AND
		// stdout TTY). When true, drop into the same sub-picker the
		// launcher uses so the human gets install / uninstall /
		// status as options instead of a dead cursor. Synthesize a
		// command{} on the fly because referencing the `commands`
		// slice from here would close the
		// commands → runMCP → lookup → commands init cycle.
		if cliprompt.IsInteractive() {
			return runSubPicker(command{name: "mcp", subs: mcpSubs, run: runMCP})
		}
		return mcp.Run(os.Stdin, os.Stdout, os.Stderr)
	}
	switch args[0] {
	case "install":
		return mcpcmd.Install(args[1:])
	case "uninstall":
		return mcpcmd.Uninstall(args[1:])
	case "status":
		return mcpcmd.Status(args[1:])
	case "help", "--help", "-h":
		// `everyapi mcp --help` used to fall through to the unknown-
		// subcommand branch and exit 2 with a confusing message;
		// keep parity with every other command's help-flag handling.
		fmt.Print(renderUsage())
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown 'mcp' subcommand %q. Try 'everyapi mcp install' to register, 'everyapi mcp uninstall' to remove, 'everyapi mcp status' to check, or 'everyapi mcp' (no args) to run the server.\n", args[0])
		os.Exit(2)
		return nil // unreachable
	}
}
