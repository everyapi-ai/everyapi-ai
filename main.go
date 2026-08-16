// everyapi — CLI for the EveryAPI AI API gateway.
//
// Covers the V1 buyer onboarding flow (auth login/logout/status, use) AND hosts the MCP server as the `mcp` subcommand — same Go module, single binary, one install. See README.md for the full command surface and the design rationale.
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

	"github.com/everyapi-ai/everyapi-ai/v3/cmd"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/admin"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/checkin"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/demand"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/dispute"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/dm"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/doctor"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/edge"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/events"
	logcmd "github.com/everyapi-ai/everyapi-ai/v3/cmd/log"
	mcpcmd "github.com/everyapi-ai/everyapi-ai/v3/cmd/mcp"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/models"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/notify"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/perf"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/proxy"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/report"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/seller"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/settings"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/subscription"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/token"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/upstream"
	usagecmd "github.com/everyapi-ai/everyapi-ai/v3/cmd/usage"
	usercmd "github.com/everyapi-ai/everyapi-ai/v3/cmd/user"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/wallet"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/mcp"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// command is a single top-level subcommand. The registry below replaces the older switch/case dispatch — adding a command is now one line in `commands`, and the help-flag handling stays uniform.
type command struct {
	name string
	// aliases lets `version` also answer to `--version` / `-v` without a special case in main. The principle is "alias = synonym", not "alias = different behavior".
	aliases []string
	// desc is the one-line summary the launcher (bare `everyapi` on a TTY) renders next to the command name. Mirrors the text in the static `usage` block — kept in sync by review, not code: the usage block targets the piped / -h reader and the launcher targets the interactive picker.
	//
	// IMPORTANT: this is the FALLBACK string for commandDesc(). The authoritative copy for the launcher picker lives in i18n under `launcher.desc.<name>` (en.toml mirrors this field; other locales translate). If you edit `desc` here, edit `en.toml`'s entry too — otherwise English users see the i18n value (stale) while non-English locales unaffected. The locale-parity test will not catch value drift, only key drift.
	desc string
	// adminOnly hides the row from the launcher when the cached credential isn't an admin user. Mirrors the admin block's gating in renderUsage. Hides UI affordances the user can't usefully exercise; the backend still 403s if a non-admin invokes the command anyway, so this is cosmetic.
	adminOnly bool
	// requireLogin hides the row from the launcher when no credentials are cached. Pure UX: typing the command at the shell still works (and errors with "not logged in"), but the menu shouldn't advertise actions that immediately fail.
	requireLogin bool
	// hideLoggedIn is the inverse — hides the row once credentials exist. Currently unused: it gated the old top-level `login` row, but login moved under `auth` (whose subs aren't individually gated). Kept as a reusable gate for a future logged-out-only top-level command.
	hideLoggedIn bool
	// subs is the subcommand menu rendered when this command is picked from the launcher (or invoked bare on a TTY without arguments). Each entry's args slice is passed verbatim to run, so a row {args: []string{"marketplace", "on"}} dispatches the same way `everyapi admin marketplace on` would. Only includes subcommands that are useful without further flags — flag-required actions (seller add-key, edge register --name N) stay command-line-only. Empty/nil means "no sub-menu — pick runs the command bare".
	subs []subcommand
	// headerFn, when set, prints a status header above the sub-picker each time the menu renders — for "stateful service" commands where the current state IS the thing you came to see (mcp: which clients are registered; proxy: is the sanitizer running). The `status` action then lives in this header instead of as a menu row, so the picker only lists the things you can DO. Reuses the command's own status printer, so it stays in sync.
	headerFn func()
	// subsFn is the dynamic alternative to subs: when set, the sub-picker calls it on every re-render instead of reading the static slice — for commands whose available actions depend on live state. proxy uses it so the menu shows start XOR stop (they're mutually exclusive) and flips the moment the user starts or stops the proxy. A command sets subs OR subsFn, not both.
	subsFn func() []subcommand
	// selfMenu marks a command that renders its OWN interactive menu inside run() (admin's operator console) rather than via the generic runSubPicker. It still earns the usage `<sub>` tag, but dispatch must NOT hand it to runSubPicker — run() drives the picker itself.
	selfMenu bool
	run      func(args []string) error
}

// hasPicker reports whether the generic runSubPicker should drive c — i.e. it has registry-declared rows (static subs or dynamic subsFn).
func (c command) hasPicker() bool { return len(c.subs) > 0 || c.subsFn != nil }

// hasSubmenu reports whether c offers ANY interactive sub-menu — a runSubPicker one OR a self-rendered console (selfMenu). Drives the usage `<sub>` tag, so admin still advertises its console even though it isn't registry-driven.
func (c command) hasSubmenu() bool { return c.hasPicker() || c.selfMenu }

// subcommand is one row in a command group's sub-menu rendered by runSubPicker. name is what the picker shows; desc is the help blurb to its right; args is what the parent command's run gets when this row is selected.
type subcommand struct {
	name string
	desc string
	args []string
}

// mcpSubs is the picker menu for `everyapi mcp`. Extracted into its own var so `runMCP` can hand it to runSubPicker when invoked bare on a TTY without referencing the `commands` slice — that ref would close a commands → runMCP → lookup → commands package-init cycle and refuse to compile.
var mcpSubs = []subcommand{
	{name: "install", desc: "Auto-register everyapi as an MCP server (default: claude)", args: []string{"install"}},
	{name: "uninstall", desc: "Remove the MCP registration", args: []string{"uninstall"}},
}

// mcpHeader prints the current MCP-client registration status — the header shown above the mcp sub-picker (the old `status` row is now this header). Reuses the same code path as `everyapi mcp status`.
func mcpHeader() { _ = runMCP([]string{"status"}) }

// proxyHeader prints the sanitizer proxy's running status above the proxy sub-picker — same code path as `everyapi proxy status`.
func proxyHeader() { _ = proxy.Run([]string{"status"}) }

// proxyMenuSubs builds the interactive proxy sub-menu, showing only the action that applies: start and stop are mutually exclusive — a running proxy can't be started and a stopped one can't be stopped — so the picker lists exactly one of them next to configure. Re-evaluated on every sub-picker re-render (see runSubPicker), so the row flips from start to stop the instant the proxy comes up, and back when it stops.
func proxyMenuSubs() []subcommand {
	toggle := subcommand{name: "start", desc: "Run the sanitizer proxy (asks background vs foreground)", args: []string{"start"}}
	if proxy.IsRunning() {
		toggle = subcommand{name: "stop", desc: "Stop the running proxy (uses PID file)", args: []string{"stop"}}
	}
	return []subcommand{
		toggle,
		{name: "configure", desc: "Interactive detector + custom-pattern setup", args: []string{"configure"}},
	}
}

// authHeader prints the session status above the auth sub-picker — the same `everyapi auth status` output (quota / usage / balance) when signed in. cmd.Status returns (without printing) a localized one-liner when signed out or expired; surface that as the header so the menu is never unheaded and the user can see why login is the only action.
func authHeader() {
	if err := cmd.Status(nil); err != nil {
		cliout.Println(cliout.Sanitize(err.Error()))
	}
}

// authMenuSubs builds the interactive auth sub-menu by login state. login and logout are mutually exclusive; status is no longer a row — it's the header (see authHeader), shown on entry. Signed out → only login; signed in → only logout. Re-evaluated on every sub-picker re-render, so the menu flips the moment the user logs in or out.
//
// Offers logout only when a credential is present AND still authenticates. An expired / revoked token leaves the credentials file in place, so the old presence-only check showed "logout" to a user who is effectively signed out and actually needs "login" — directly contradicting the "session expired" header rendered right above it. sessionRejected returns false for a nil / legacy (no user_id) credential or any transport error, so those keep the historical logout row rather than false-walling the user on a flaky probe.
func authMenuSubs() []subcommand {
	creds, _ := config.Load()
	return authMenuSubsFor(creds, sessionRejected)
}

// authMenuSubsFor is the testable core of authMenuSubs: it owns the login-vs-logout decision given the loaded credential and a session probe, with the disk read (config.Load) and the real network probe (sessionRejected) injected by the caller. logout requires a present credential whose session the probe does NOT reject.
func authMenuSubsFor(creds *config.Credentials, rejected func(*config.Credentials) bool) []subcommand {
	if creds != nil && !rejected(creds) {
		return []subcommand{
			{name: "logout", desc: "Remove this device's credentials", args: []string{"logout"}},
		}
	}
	return []subcommand{
		{name: "login", desc: "Authenticate this device with EveryAPI", args: []string{"login"}},
	}
}

// versionHeader prints the build version — the header above the version sub-picker (update / uninstall).
func versionHeader() { _ = cmd.Version(nil) }

// walletRun folds the former top-level `topup` into wallet: `everyapi wallet topup` routes to cmd.Topup; everything else (history / info / bare) goes to the wallet package unchanged.
func walletRun(args []string) error {
	if len(args) > 0 && args[0] == "topup" {
		return cmd.Topup(args[1:])
	}
	return wallet.Run(args)
}

func isHelpArg(s string) bool { return s == "help" || s == "--help" || s == "-h" }

// accountRun folds user + subscription into one `account` namespace. Their leaf actions have unique names, so they flatten directly (`everyapi account 2fa`, `everyapi account plans`). The full leaf set of each command is routed — not just the few shown in the launcher — so nothing the old top-level `user`/`subscription` could do is lost. The explicit `user`/`subscription` arms are a forward-proof wrap: any leaf added to those commands later stays reachable via `everyapi account user <new-sub>` without touching this switch.
func accountRun(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		cliout.Println(i18n.T("account.usage"))
		return nil
	}
	switch args[0] {
	case "user":
		return usercmd.Run(args[1:])
	case "subscription":
		return subscription.Run(args[1:])
	case "info", "2fa", "passkey", "oauth", "update", "passwd", "setting", "aff":
		return usercmd.Run(args)
	case "plans", "self", "preference":
		return subscription.Run(args)
	default:
		cliout.Println(i18n.T("account.usage"))
		return fmt.Errorf(i18n.T("account.unknown_sub"), args[0])
	}
}

// statsRun groups the read-only observability commands. usage/perf/ upstream are leaves; log keeps its own subs (`stats log list`). Not requireLogin — perf/upstream work logged-out; usage/log error on their own if not.
func statsRun(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		cliout.Println(i18n.T("stats.usage"))
		return nil
	}
	switch args[0] {
	case "usage":
		return usagecmd.Run(args[1:])
	case "perf":
		return perf.Run(args[1:])
	case "upstream":
		return upstream.Run(args[1:])
	case "log":
		return logcmd.Run(args[1:])
	default:
		cliout.Println(i18n.T("stats.usage"))
		return fmt.Errorf(i18n.T("stats.unknown_sub"), args[0])
	}
}

// marketRun groups the buyer-side marketplace commands (demand / dispute) plus abuse reports. demand/dispute keep their subs (`market demand list`).
func marketRun(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		cliout.Println(i18n.T("market.usage"))
		return nil
	}
	switch args[0] {
	case "demand":
		return demand.Run(args[1:])
	case "dispute":
		return dispute.Run(args[1:])
	case "report":
		return report.Run(args[1:])
	default:
		cliout.Println(i18n.T("market.usage"))
		return fmt.Errorf(i18n.T("market.unknown_sub"), args[0])
	}
}

// inboxRun groups notifications + direct messages. Both keep their subs (`inbox notify list`, `inbox dm threads`).
func inboxRun(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		cliout.Println(i18n.T("inbox.usage"))
		return nil
	}
	switch args[0] {
	case "notify":
		return notify.Run(args[1:])
	case "dm":
		return dm.Run(args[1:])
	default:
		cliout.Println(i18n.T("inbox.usage"))
		return fmt.Errorf(i18n.T("inbox.unknown_sub"), args[0])
	}
}

// versionRun dispatches `everyapi version [update|uninstall]`. Bare prints the version (same as the --version/-v flags); update/uninstall route to their commands. help/unknown print usage.
func versionRun(args []string) error {
	if len(args) == 0 {
		return cmd.Version(nil)
	}
	switch args[0] {
	case "update":
		return cmd.Update(args[1:])
	case "uninstall":
		return cmd.Uninstall(args[1:])
	case "help", "--help", "-h":
		cliout.Println(i18n.T("version.usage"))
		return nil
	default:
		cliout.Println(i18n.T("version.usage"))
		return fmt.Errorf(i18n.T("version.unknown_sub"), args[0])
	}
}

// commands is the registered set, in the order they appear in the help text. main() walks this slice (not a map) so the lookup order matches the documented order — keeps the "which command runs when two names conflict" question impossible.
var commands = []command{
	// Session commands live under `auth` (everyapi auth login|logout| status) so the top level isn't cluttered with them. The parent carries no requireLogin/hideLoggedIn gate — it must stay visible in both auth states. Entering it shows the session status as a header (authHeader); authMenuSubs then offers the one applicable action (login when signed out, logout when signed in).
	{name: "auth", desc: "Sign in / out, session status", run: cmd.Auth, headerFn: authHeader, subsFn: authMenuSubs},
	{name: "wallet", desc: "Top-up · payment history · methods · redemption keys", requireLogin: true, run: walletRun, subs: []subcommand{
		{name: "topup", desc: "Open the wallet top-up page (anti-phishing verification phrase)", args: []string{"topup"}},
		{name: "history", desc: "Paginated payment history", args: []string{"history"}},
		{name: "info", desc: "Enabled payment methods + suggested amounts", args: []string{"info"}},
	}},
	{name: "checkin", desc: "Claim today's daily-grant quota", requireLogin: true, run: checkin.Run, subs: []subcommand{
		{name: "claim", desc: "Claim today's reward", args: []string{"claim"}},
		{name: "status", desc: "Show this month's check-in calendar", args: []string{"status"}},
	}},
	{name: "account", desc: "Profile / 2FA / aff · subscription plans / billing", requireLogin: true, run: accountRun, subs: []subcommand{
		{name: "info", desc: "Rolled-up profile + security view", args: []string{"info"}},
		{name: "2fa", desc: "2FA status", args: []string{"2fa"}},
		{name: "aff", desc: "Show affiliate code", args: []string{"aff"}},
		{name: "plans", desc: "List enabled subscription plans", args: []string{"plans"}},
		{name: "self", desc: "Show your subscriptions", args: []string{"self"}},
	}},
	{name: "use", desc: "Launch a third-party CLI (claude / codex / opencode / gemini / grok / qwen-code / kimi-code / hermes) via EveryAPI", requireLogin: true, run: cmd.Use},
	{name: "token", desc: "Manage relay API tokens (list / create / key / revoke / …)", requireLogin: true, run: token.Run, subs: []subcommand{
		// Only flag-free verbs surface in the launcher picker; the rest (create/update/key/revoke/enable/disable/show) need flags or an id and stay command-line-only.
		{name: "list", desc: "List your tokens (masked keys)", args: []string{"list"}},
		{name: "switch", desc: "Choose the default API key used by the CLI", args: []string{"switch"}},
	}},
	{name: "stats", desc: "Usage / request log / model perf / upstream health", run: statsRun, subs: []subcommand{
		{name: "usage", desc: "Day-by-day quota usage", args: []string{"usage"}},
		{name: "log list", desc: "Recent log entries (newest first)", args: []string{"log", "list"}},
		{name: "log stat", desc: "Quota / RPM / TPM totals for the window", args: []string{"log", "stat"}},
		{name: "log summary", desc: "Per-model spend over the last 7d", args: []string{"log", "summary"}},
		{name: "perf", desc: "Per-model performance (success rate / latency / throughput)", args: []string{"perf"}},
		{name: "upstream", desc: "Upstream provider health (status-page rollup)", args: []string{"upstream"}},
	}},
	{name: "models", desc: "Model catalog: list / pricing / groups", requireLogin: true, run: models.Run, subs: []subcommand{
		{name: "list", desc: "Print every model id your group can route to", args: []string{"list"}},
		{name: "pricing", desc: "Per-model rate sheet", args: []string{"pricing"}},
		{name: "groups", desc: "Routing groups your account can use", args: []string{"groups"}},
	}},
	{name: "market", desc: "Demand posts · disputes · abuse reports", requireLogin: true, run: marketRun, subs: []subcommand{
		{name: "demand list", desc: "Public marketplace feed", args: []string{"demand", "list"}},
		{name: "demand my", desc: "Demands you've posted", args: []string{"demand", "my"}},
		{name: "dispute my", desc: "List your open + resolved disputes", args: []string{"dispute", "my"}},
		{name: "report", desc: "File an abuse / TOS-violation report", args: []string{"report"}},
	}},
	{name: "inbox", desc: "In-app notifications · direct messages", requireLogin: true, run: inboxRun, subs: []subcommand{
		{name: "notify list", desc: "Recent notifications", args: []string{"notify", "list"}},
		{name: "notify count", desc: "Just the unread count", args: []string{"notify", "count"}},
		{name: "notify readall", desc: "Flip every unread to read", args: []string{"notify", "readall"}},
		{name: "dm threads", desc: "Your DM threads", args: []string{"dm", "threads"}},
		{name: "dm contacts", desc: "Users you've messaged", args: []string{"dm", "contacts"}},
		{name: "dm count", desc: "Unread DM count", args: []string{"dm", "count"}},
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
		{name: "rename", desc: "Rename or relocate a node (server-side; updates dashboard label)", args: []string{"rename"}},
		{name: "pause", desc: "Manually disable a node — sticky across reconnects until 'resume'", args: []string{"pause"}},
		{name: "resume", desc: "Clear a manual pause — node rejoins routing on next heartbeat", args: []string{"resume"}},
		{name: "remove", desc: "Remove the active node + delete backend row", args: []string{"remove"}},
	}},
	// admin has no static subs: bare `everyapi admin` on a TTY launches admin's own two-level operator console (area → action → inline arg prompts) inside the admin package, since the keyed actions need values a flat picker row can't carry. selfMenu earns it the usage <sub> tag; dispatchInteractive sees hasPicker()==false and calls admin.Run (not runSubPicker), which handles the TTY/non-TTY split.
	{name: "admin", desc: "Operator commands", adminOnly: true, requireLogin: true, selfMenu: true, run: admin.Run},
	{name: "proxy", desc: "Local sanitizer proxy (privacy filter for SDK requests)", run: proxy.Run, headerFn: proxyHeader, subsFn: proxyMenuSubs},
	{name: "mcp", desc: "MCP server for AI CLIs (Claude Code / Codex / Gemini)", run: runMCP, headerFn: mcpHeader, subs: mcpSubs},
	{name: "doctor", desc: "Self-check (creds, gateway, sanitizer, tools)", run: doctor.Run},
	{name: "events", desc: "Subscribe to the live event stream (SSE)", requireLogin: true, run: events.Run},
	{name: "settings", desc: "View / change CLI preferences (language, …)", run: settings.Run},
	// `version` shows the build version as a header, then offers the CLI-lifecycle actions (update / uninstall) as the menu. The bare `everyapi version` (and the --version/-v flags, special-cased in main) just print the version.
	{name: "version", aliases: []string{"--version", "-v"}, desc: "Build version · update · uninstall", headerFn: versionHeader, run: versionRun, subs: []subcommand{
		{name: "update", desc: "Check for a newer release and run the matching upgrade", args: []string{"update"}},
		{name: "uninstall", desc: "Remove everyapi state and binary from this machine", args: []string{"uninstall"}},
	}},
}

// renderUsage builds the `everyapi help` / non-TTY usage text. The command list is GENERATED from the `commands` registry (grouped by category, descriptions via commandDesc) rather than hand-maintained in the locale files — so it can never drift from the actual command set, and adding/moving a command needs no help-text edit. Only the header/footer prose is localized (launcher.usage_header/_footer). The admin group is included only for admin users (admin commands are adminOnly; usageCommandList drops them otherwise).
func renderUsage() string {
	creds, err := config.Load()
	isAdmin := err == nil && creds != nil && creds.IsAdmin()
	var b strings.Builder
	b.WriteString(i18n.T("launcher.usage_header"))
	b.WriteString("\n")
	b.WriteString(usageCommandList(isAdmin))
	b.WriteString(i18n.T("launcher.usage_footer"))
	b.WriteString("\n")
	// **keyword** markers render bold on a styled terminal and strip to plain text when piped / NO_COLOR.
	return style.Emph(b.String())
}

// usageCommandList renders the registry as a grouped, aligned command list for renderUsage. Mirrors the launcher's categories (launcherGroupOrder + groupTitle) and reuses commandDesc for the localized one-liners. adminOnly rows are dropped for non-admins.
func usageCommandList(isAdmin bool) string {
	var shown []command
	maxName := 0
	for _, c := range commands {
		if c.adminOnly && !isAdmin {
			continue
		}
		shown = append(shown, c)
		if len(c.name) > maxName {
			maxName = len(c.name)
		}
	}
	byGroup := make(map[string][]command, len(launcherGroupOrder))
	for _, c := range shown {
		byGroup[groupOf(c.name)] = append(byGroup[groupOf(c.name)], c)
	}
	// Align descriptions past the widest "name <sub>" cell. " <sub>" (6 cols) marks commands that have a subcommand menu.
	const subTag = " <sub>"
	descCol := maxName + len(subTag)
	var b strings.Builder
	for _, key := range launcherGroupOrder {
		cs := byGroup[key]
		if len(cs) == 0 {
			continue
		}
		b.WriteString("\n" + groupTitle(key) + "\n")
		for _, c := range cs {
			tag := ""
			if c.hasSubmenu() {
				tag = subTag
			}
			pad := strings.Repeat(" ", descCol-len(c.name)-len(tag))
			b.WriteString("  " + style.Bold(c.name) + tag + pad + "  " + commandDesc(c) + "\n")
		}
	}
	return b.String()
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

// runLauncher is the bare-`everyapi` interactive entry point: shows every visible command in a huh-backed picker, then dispatches the chosen one with no extra args. Each command's own no-arg handler takes over from there (e.g. `use` opens its tool picker, `seller` renders its subcommand help / picker).
//
// Hidden from non-admin users:
//   - rows marked adminOnly
//   - --version / -v aliases (the canonical "version" row stays)
//
// resolveLanguage publishes the user's preferred language to both the in-process i18n table and EVERYAPI_LANG so SDK calls attach Accept-Language. See the doc on main() for the precedence chain.
func resolveLanguage() {
	lang := ""
	if s, err := config.LoadSettings(); err == nil && s != nil {
		lang = s.Language
	}
	if lang == "" {
		lang = i18n.DetectFromEnv()
	}
	i18n.SetLanguage(lang)
	// Export the RESOLVED canonical tag, not the raw settings value: SetLanguage normalizes (folds zh_CN→zh, zh-Hant→zh-TW, drops unsupported tags to en), and the SDK forwards EVERYAPI_LANG verbatim as Accept-Language. Exporting the raw value would let the wire header diverge from the language the CLI resolved to. i18n.Language() is always non-empty after SetLanguage.
	_ = os.Setenv("EVERYAPI_LANG", i18n.Language())
}

// Esc / Ctrl-C from a NESTED picker (a tool picker, group picker, confirm dialog, etc. surfaced by the dispatched command) returns here and re-renders the launcher — that's the "back to parent level" affordance. Esc / Ctrl-C from the launcher itself exits cleanly with status 0.
//
// The menu is rebuilt every loop iteration from the current credentials, so logging in / out from inside the launcher refreshes the visible rows without re-spawning the process. On first render an entry probe (sessionRejected) confirms the cached token still authenticates — a revoked / expired token drops the menu to its logged-out shape instead of advertising commands that would only 401.
func runLauncher() error {
	// Localize the grouped picker's nav-hint footer once up front (language is already resolved by main → resolveLanguage).
	cliprompt.SetMenuNavHint(i18n.T("launcher.nav_hint"))
	// sessionDead latches once the backend definitively rejects the cached token (the entry probe below). The credentials file is left intact — `login` overwrites it — but for the rest of this launcher session we render the logged-out menu so the user isn't stuck picking commands that all fail with 401.
	sessionDead := false
	probed := false
	lastSel := 0
	for {
		creds, _ := config.Load()
		loggedIn := creds != nil && !sessionDead

		// Entry probe: once, on the first render, only when the cached credentials still claim a live session. A definitive 401 latches sessionDead so the menu drops to logged-out; a network error / 5xx is deliberately NOT a verdict (see sessionRejected) so an offline launch keeps the cached menu.
		if loggedIn && !probed {
			probed = true
			if sessionRejected(creds) {
				sessionDead = true
				loggedIn = false
				fmt.Fprintln(os.Stderr, i18n.T("auth.session_expired"))
			}
		}
		isAdmin := loggedIn && creds.IsAdmin()

		sections, maxName := launcherSections(loggedIn, isAdmin)
		var chosen command
		if menuLayout() == menuLayoutNested {
			// Nested: category picker → command picker. Esc at the category level cancels the whole launcher (same as Esc on the flat picker did); Esc inside a category goes back up.
			c, perr := pickCommandNested(sections, maxName)
			if perr != nil {
				if errors.Is(perr, cliprompt.ErrPickCancelled) {
					return nil
				}
				return perr
			}
			chosen = c
		} else {
			// Grouped single screen. Flatten the sections back into the parallel (groups, commands) shape PickGrouped expects; its returned flat index maps straight into `flat`.
			var groups []cliprompt.MenuGroup
			var flat []command
			for _, s := range sections {
				groups = append(groups, cliprompt.MenuGroup{Title: s.title, Labels: s.labels})
				flat = append(flat, s.cmds...)
			}
			// The row set shrinks when the probe (or a logout) flips the menu to logged-out; clamp the remembered cursor so it can't index past the rebuilt slice.
			if lastSel >= len(flat) {
				lastSel = 0
			}
			idx, perr := cliprompt.PickGrouped(i18n.T("launcher.welcome"), groups, lastSel)
			if perr != nil {
				if errors.Is(perr, cliprompt.ErrPickCancelled) {
					return nil
				}
				return perr
			}
			lastSel = idx
			chosen = flat[idx]
		}
		err := dispatchInteractive(chosen, nil)
		// After visiting `auth` (login lives under it now), re-derive the session on the next iteration so the rebuilt menu reflects the new state. Clearing sessionDead alone isn't enough — the entry probe is one-shot (`probed`), so without re-arming it a login wouldn't be re-verified; clearing both forces a fresh probe of the (possibly changed) credentials. Crucially this is NOT gated on err == nil: leaving the auth sub-picker via Esc / the back row returns ErrPickCancelled (benign navigation, not failure), and the old `&& err == nil` gate left the launcher stuck on the logged-out menu after a successful login followed by Esc.
		if chosen.name == "auth" {
			sessionDead = false
			probed = false
		}
		// Stay in the menu regardless of how the dispatched command returned. Real errors (not-logged-in, transient API failure, etc.) print to stderr and the loop re-renders, so the user can pick 'login' or some other command without re-spawning the CLI. Without this, leaf commands that surface errors (status, topup) eject the user from the launcher and leaf commands that print friendly messages and return nil (proxy status when nothing's running) don't — same menu, two contradictory exit semantics.
		if err != nil &&
			!errors.Is(err, cliprompt.ErrPickCancelled) &&
			!errors.Is(err, io.EOF) {
			fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), err)
		}
		cliout.Println("")
	}
}

// nameCell right-pads name to width w with PLAIN spaces, then bolds only the name text. Padding stays outside the bold span so the ANSI bytes never enter the %-*s-style width math — column alignment holds in the picker. Command names are ASCII, so len == display width.
func nameCell(name string, w int) string {
	pad := ""
	if w > len(name) {
		pad = strings.Repeat(" ", w-len(name))
	}
	return style.Bold(name) + pad
}

// launcherRows builds the visible command set and their aligned display labels for the given auth state. Split out of runLauncher so the loop can rebuild the menu each iteration — the row set changes when the user logs in / out or when the entry probe finds a stale token.
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

const (
	menuLayoutGrouped = "grouped"
	menuLayoutNested  = "nested"
)

// launcherGroupOrder is the display order of the launcher's command categories — high-frequency buyer commands first, role/utility surfaces last. Each key resolves to a localized title via `launcher.group.<key>`.
var launcherGroupOrder = []string{"account", "api", "marketplace", "tools", "admin"}

// commandGroup maps every top-level command name to its launcher category. Kept as a side table (not a field on command) so the big registry literal stays untouched. TestEveryCommandGrouped asserts this covers the registry exactly — a new command with no entry here fails that test rather than silently landing in the fallback bucket.
var commandGroup = map[string]string{
	// Account & billing (auth=login/logout/status; account=user+ subscription; wallet absorbs topup — see the dispatchers)
	"auth": "account", "account": "account", "wallet": "account", "checkin": "account",
	// Using the API
	"use": "api", "token": "api", "models": "api",
	// (stats merged into the API/data category — no separate insights group)
	"stats": "api",
	// Marketplace · messages · supply (market/inbox + seller/edge)
	"market": "marketplace", "inbox": "marketplace",
	"seller": "marketplace", "edge": "marketplace",
	// Tools & settings
	"mcp": "tools", "proxy": "tools",
	"doctor": "tools", "events": "tools", "settings": "tools",
	"version": "tools", // version namespace = build version + update/uninstall
	// Admin
	"admin": "admin",
}

// groupOf returns a command's category key, defaulting to "tools" for any command missing from commandGroup (defensive — the test keeps the map exhaustive, so this fallback shouldn't fire in practice).
func groupOf(name string) string {
	if g, ok := commandGroup[name]; ok {
		return g
	}
	return "tools"
}

// groupTitle resolves a category's localized header, falling back to the raw key if the locale is missing it (i18n.T returns the key).
func groupTitle(key string) string {
	if t := i18n.T("launcher.group." + key); t != "launcher.group."+key {
		return t
	}
	return key
}

// menuSection is one rendered category: a localized title plus the parallel label/command rows under it. labels are the aligned "name  desc" strings from launcherRows (so column alignment is shared across categories), cmds the commands they dispatch.
type menuSection struct {
	title  string
	labels []string
	cmds   []command
}

// launcherSections partitions the visible command set into ordered categories. It reuses launcherRows for the auth filtering and the global name-column alignment, then buckets the parallel slices by groupOf — empty categories are dropped.
func launcherSections(loggedIn, isAdmin bool) ([]menuSection, int) {
	visible, labels := launcherRows(loggedIn, isAdmin)
	maxName := 0
	for _, c := range visible {
		if len(c.name) > maxName {
			maxName = len(c.name)
		}
	}
	idxByGroup := make(map[string][]int, len(launcherGroupOrder))
	for i, c := range visible {
		g := groupOf(c.name)
		idxByGroup[g] = append(idxByGroup[g], i)
	}
	var sections []menuSection
	for _, key := range launcherGroupOrder {
		ix := idxByGroup[key]
		if len(ix) == 0 {
			continue
		}
		s := menuSection{title: groupTitle(key)}
		for _, i := range ix {
			s.labels = append(s.labels, labels[i])
			s.cmds = append(s.cmds, visible[i])
		}
		sections = append(sections, s)
	}
	return sections, maxName
}

// backRowLabel renders the sub-picker "go up" row in the same two-column shape as the command rows: the localized back word in the (bold) name column, an arrow hint in the description column. Padding is by display width (style.Width), so the wide CJK back word still aligns its hint with the command descriptions.
func backRowLabel(maxName int) string {
	word := i18n.T("common.back")
	pad := maxName - style.Width(word)
	if pad < 0 {
		pad = 0
	}
	return style.Bold(word) + strings.Repeat(" ", pad) + "  " + i18n.T("common.back_hint")
}

// menuLayout reads the persisted launcher layout preference. Defaults to grouped; any unrecognized value (incl. a missing / corrupt settings file) also falls back to grouped.
func menuLayout() string {
	if s, err := config.LoadSettings(); err == nil && s != nil && s.MenuLayout == menuLayoutNested {
		return menuLayoutNested
	}
	return menuLayoutGrouped
}

// pickCommandNested drives the two-level (B) launcher: a category picker, then the command picker for the chosen category. Esc inside a category returns to the category list; Esc at the category level returns ErrPickCancelled so the caller exits the launcher. A trailing "back" row in each category mirrors the Esc-to-go-up affordance.
func pickCommandNested(sections []menuSection, maxName int) (command, error) {
	titles := make([]string, len(sections))
	for i, s := range sections {
		titles[i] = s.title
	}
	gsel := 0
	for {
		gidx, err := cliprompt.PickWithSelected(i18n.T("launcher.pick_category"), titles, gsel)
		if err != nil {
			return command{}, err // Esc here cancels the launcher
		}
		gsel = gidx
		s := sections[gidx]
		rows := append(append([]string{}, s.labels...), backRowLabel(maxName))
		cidx, cerr := cliprompt.PickWithSelected(s.title, rows, 0)
		if cerr != nil {
			if errors.Is(cerr, cliprompt.ErrPickCancelled) {
				continue // Esc inside a category → back to the category list
			}
			return command{}, cerr
		}
		if cidx == len(s.labels) {
			continue // the "back" row
		}
		return s.cmds[cidx], nil
	}
}

// commandDesc resolves the launcher row's description. Looks up `launcher.desc.<name>` in the current locale's table; falls back to the hardcoded English `c.desc` when the key is missing (i18n.T returns the bare key in that case — using it directly would print "launcher.desc.login" to the user). The struct field stays the source of truth for the English copy; this helper just plumbs the translated variant through when one exists.
func commandDesc(c command) string {
	key := "launcher.desc." + c.name
	if v := i18n.T(key); v != key {
		return style.Emph(v)
	}
	return style.Emph(c.desc)
}

// subcommandDesc is the sub-picker equivalent of commandDesc. The key is the PARENT's name + the sub-row's args joined with underscores (so admin's "marketplace status" becomes `launcher.subs.admin.marketplace_status`), because the sub `name` field is a display string that may contain spaces — args is the stable identifier.
//
// The `slug == ""` branch is defensive: every current subcommand in the registry sets a non-empty args slice, but a future entry that only wires up a `run` function with no args (e.g. a top-level shortcut row) would otherwise generate a `launcher.subs.X.` key with a trailing dot. Falling back to a space-stripped name keeps the resulting key human-readable.
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

// launcherProbeTimeout caps the entry-probe round-trip. The SDK's http.Client.Timeout is 30s — far too long to stall a menu render behind. 3s clears a healthy request yet keeps an offline launch responsive.
const launcherProbeTimeout = 3 * time.Second

// sessionRejected reports whether the backend DEFINITIVELY rejects the cached credentials — GET /api/user/self answering HTTP 401. Every other outcome (timeout, DNS failure, 5xx, success) returns false: "couldn't verify" must never masquerade as "logged out", or a transient blip walls the user behind a `login` that itself needs the network.
func sessionRejected(creds *config.Credentials) bool {
	// Legacy credentials predate the user_id field. Without it the request omits the EveryAPI-User-Id header and UserAuth returns 401 "user ID not provided" — indistinguishable here from a bad token. Skip the probe: a stale logged-in menu for a pre-user_id credential beats falsely walling the user out.
	if creds == nil || creds.UserID <= 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), launcherProbeTimeout)
	defer cancel()
	_, err := api.ForCredentials(creds).
		GetSelf(ctx)
	return api.IsUnauthorized(err)
}

// dispatchInteractive is the single entry point for "run a command the way an interactive user would expect". If the command has a subs menu and the user hasn't already typed a subcommand on the argv, render the sub-picker. Otherwise dispatch verbatim.
//
// The sub-picker has the same back-on-cancel UX as the launcher: Esc returns the sentinel ErrPickCancelled so the CALLER's loop (launcher or main) can re-render the parent menu instead of exiting the process.
//
// Non-TTY callers (CI / piped) skip the picker — they get the command's original "no subcommand specified" usage text exactly the way it printed before any of this picker code existed.
func dispatchInteractive(c command, args []string) error {
	// Only runSubPicker-driven commands (subs/subsFn) are intercepted here; a selfMenu command (admin) falls through to c.run, which renders its own console. hasPicker — not hasSubmenu — gates this.
	if len(args) > 0 || !c.hasPicker() || !cliprompt.IsInteractive() {
		return c.run(args)
	}
	return runSubPicker(c)
}

// runSubPicker renders a huh-backed Select over c.subs and dispatches the chosen row's args to c.run. Loops on ErrPickCancelled from the dispatched action (so cancelling a nested prompt re-renders the sub-picker), returns ErrPickCancelled itself when the user cancels the sub-picker (so the caller — runLauncher or main — re-renders THE PARENT).
//
// A trailing "back" row gives that unwind a visible affordance. Esc already returns ErrPickCancelled, but the binding is invisible (runHuhField sets WithShowHelp(false)), so a user who never learned Esc was stranded in the sub-menu — they could only re-run claim / status, never climb back to the launcher. Selecting the back row raises the same ErrPickCancelled the key does.
func runSubPicker(c command) error {
	prompt := fmt.Sprintf(i18n.T("common.pick_subcommand"), c.name)
	lastSel := 0
	for {
		// Resolve the rows fresh each iteration: subsFn-backed commands (proxy) change their available actions with live state, so the menu must rebuild — not just the header — after every action.
		subs := c.subs
		if c.subsFn != nil {
			subs = c.subsFn()
		}
		maxName := 0
		for _, s := range subs {
			if len(s.name) > maxName {
				maxName = len(s.name)
			}
		}
		// Declared subs first, then the back row at index len(subs).
		backIdx := len(subs)
		labels := make([]string, len(subs)+1)
		for i, s := range subs {
			labels[i] = nameCell(s.name, maxName) + "  " + subcommandDesc(c.name, s)
		}
		labels[backIdx] = backRowLabel(maxName)
		// The row count can shrink between iterations (proxy start/stop keeps it constant, but be defensive); clamp the remembered selection so PickWithSelected never indexes out of range.
		if lastSel > backIdx {
			lastSel = backIdx
		}

		// Stateful commands (mcp/proxy) print their current status above the menu, refreshed each loop so it reflects the last action.
		if c.headerFn != nil {
			c.headerFn()
			cliout.Println("")
		}
		idx, err := cliprompt.PickWithSelected(prompt, labels, lastSel)
		if err != nil {
			return err
		}
		if idx == backIdx {
			return cliprompt.ErrPickCancelled
		}
		lastSel = idx
		err = c.run(subs[idx].args)
		// The auth sub-picker's login/logout rows are a mutually- exclusive toggle (see authMenuSubs): the instant login (or logout) returns, the menu rebuilds with the OPPOSITE action as its single row and re-parks the cursor on it. After a fresh login that opposite is `logout` — one stray Enter signs the user straight back out, the terrible UX this guards against. So once an auth action completes cleanly, unwind to the root launcher (ErrPickCancelled is the "go up one level" signal) instead of re-rendering this picker; the root menu already reflects the new session state. A FAILED action (e.g. a login network error) is deliberately not "clean" — it falls through so the user stays here and can retry.
		if c.name == "auth" &&
			(err == nil ||
				errors.Is(err, cliprompt.ErrPickCancelled) ||
				errors.Is(err, io.EOF)) {
			return cliprompt.ErrPickCancelled
		}
		// Same "stay in the menu" rule as runLauncher: real errors get printed to stderr but don't eject the user from this sub-picker — they can pick something else or Esc to go up. Esc from THIS picker (handled above) bubbles up so the launcher re-renders the parent menu.
		if err != nil &&
			!errors.Is(err, cliprompt.ErrPickCancelled) &&
			!errors.Is(err, io.EOF) {
			fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), err)
		}
		cliout.Println("")
	}
}

func main() {
	// Resolve the user's language preference once at startup and publish it two ways: i18n.SetLanguage for CLI-originated strings, EVERYAPI_LANG env for the SDK to attach as Accept-Language on every API call (so backend errors come back translated). Resolution chain (first-wins):
	//   1. settings.json's `language` field
	//   2. EVERYAPI_LANG / LC_ALL / LC_MESSAGES / LANG env
	//   3. "en"
	// A broken / missing settings file falls through to the env chain rather than failing startup — the user shouldn't be locked out of `everyapi auth login` because the preference file is corrupt.
	resolveLanguage()

	if len(os.Args) < 2 {
		if cliprompt.IsInteractive() {
			// The bare-`everyapi` launcher is the primary interactive surface — a user who only ever types `everyapi` and picks from the menu would otherwise never reach the auto-update check (it only fronts explicit-command dispatch below). Empty commandName is intentional: it isn't in updateCheckSkipCommands, so the check runs. "Update now" returns true → we ran the upgrade, skip the launcher.
			if cmd.MaybePromptUpdate("") {
				return
			}
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
	// Private sidecar surface for EveryAPI Connect. It intentionally bypasses the public command registry, help, launcher, login gate, and update prompt: the desktop opens it in a terminal solely to run a registry-pinned tool installer. InstallTool never continues into `use` or launches the client.
	if name == "desktop-install-tool" {
		if err := cmd.InstallTool(args); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), cliout.Sanitize(err.Error()))
			os.Exit(1)
		}
		return
	}

	if name == "help" || name == "--help" || name == "-h" {
		fmt.Print(renderUsage())
		return
	}

	// --version / -v are quick "just print the version" flags — they resolve to the `version` command but must NOT drop into its update/uninstall sub-picker on a TTY.
	if name == "--version" || name == "-v" {
		_ = cmd.Version(nil)
		return
	}

	c, ok := lookup(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, renderUsage())
		os.Exit(2)
	}

	// Auto-update check: cached-only, interactive-only. If a newer release is cached and the user picks "update now", we run the upgrade and skip the original command (an old binary running right after a brew upgrade kicked off is asking for trouble). "Later" / "skip this version" / non-TTY / dev build / opt-out env all fall through to the original command unchanged.
	if cmd.MaybePromptUpdate(name) {
		return
	}

	err := dispatchInteractive(c, args)
	if err == nil {
		return
	}
	// flag.ErrHelp bubbles up when any flag.FlagSet down the call tree sees --help / -h. The FlagSet has already printed its usage to stderr; exit cleanly rather than dress "flag: help requested" up as an Error: line. Makes `everyapi <cmd> [<sub>...] --help` reach the user as help text instead of as a noisy failure at every level.
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	// User cancelled an interactive prompt (Esc / Ctrl-C) inside the dispatched command. Don't treat it as an error worth printing to stderr — fall through to the launcher so the user can pick a different command without a fresh shell invocation. Matches the mental model "Esc = up one level" regardless of how the CLI was entered.
	if cliprompt.IsInteractive() &&
		(errors.Is(err, cliprompt.ErrPickCancelled) || errors.Is(err, io.EOF)) {
		if lerr := runLauncher(); lerr != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), cliout.Sanitize(lerr.Error()))
			os.Exit(1)
		}
		return
	}
	// Backend-relayed error messages can carry attacker-chosen ESC/CSI/OSC bytes (cliout.Sanitize's doc lists "error messages" as untrusted). Neutralize here so every command's server-error output is safe, regardless of whether the command sanitized its own error path.
	fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), cliout.Sanitize(err.Error()))
	os.Exit(1)
}

// runMCP is the `mcp` family dispatcher. Three shapes:
//
//	everyapi mcp                       → run the JSON-RPC server on stdio everyapi mcp install [client]      → register via `<client> mcp add` everyapi mcp uninstall [client]    → unregister via `<client> mcp remove`
//
// Kept here (not in cmd/mcp) so the server entry point (internal/mcp) and the install/uninstall handlers (cmd/mcp) don't have to know about each other — main wires both.
func runMCP(args []string) error {
	if len(args) == 0 {
		// Two callers reach this branch: (a) An MCP client (Claude Desktop, codex, gemini) that spawned us through a pipe and wants to speak JSON-RPC over stdio. stdin is NOT a TTY in this case. (b) A human who typed `everyapi mcp` at a shell prompt — both stdin and stdout are TTYs. Used to silently block on stdin.Read() forever because the server was waiting for an LSP-style request; looked indistinguish- able from a hang.
		//
		// Differentiate via cliprompt.IsInteractive (both stdin AND stdout TTY). When true, drop into the same sub-picker the launcher uses so the human gets install / uninstall / status as options instead of a dead cursor. Synthesize a command{} on the fly because referencing the `commands` slice from here would close the commands → runMCP → lookup → commands init cycle.
		if cliprompt.IsInteractive() {
			return runSubPicker(command{name: "mcp", subs: mcpSubs, run: runMCP, headerFn: mcpHeader})
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
		// mcp-specific usage (not the whole-CLI renderUsage — that would also reintroduce a commands→runMCP→renderUsage→commands init cycle now that renderUsage reads the registry).
		fmt.Print("everyapi mcp — MCP server for AI CLIs\n\n" +
			"USAGE\n" +
			"  everyapi mcp                    run the stdio MCP server (for AI clients)\n" +
			"  everyapi mcp install [client]   register everyapi with a client (claude/codex/gemini/librefang)\n" +
			"  everyapi mcp uninstall [client] remove the registration\n" +
			"  everyapi mcp status             show which clients have everyapi registered\n")
		return nil
	default:
		fmt.Fprintf(os.Stderr, "Try 'everyapi mcp install' to register, 'everyapi mcp uninstall' to remove, 'everyapi mcp status' to check, or 'everyapi mcp' (no args) to run the server.\n")
		return fmt.Errorf("unknown 'mcp' subcommand %q", args[0])
	}
}
