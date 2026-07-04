package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/everyapi-ai/everyapi-sdk/sanitizer"
)

// useUsage is split so the prose can embed literal backticks (e.g.
// "`-- <flag>`") without colliding with the raw-string delimiter.
const useUsage = `everyapi use — launch a third-party CLI routed through EveryAPI

USAGE
  everyapi use [<tool>] [--group <name> | --channel <name>] [--model <id>] [--sanitize] [-- tool args...]

ARGUMENTS
  <tool>                 claude | codex | gemini | hermes
                         minimax | qwen | deepseek | byteplus | glm | kimi
                         The second row launches Claude Code against that
                         provider (routed via EveryAPI) and pops a picker over
                         the provider's models — or pass --model <id> to skip it.
                         Omit to open an interactive picker over installed tools.

FLAGS
  --group <name>         Relay via the key bound to that routing group.
  --channel <name>       Alias of --group.
  (bare --group/--channel, no value) → interactive picker over your
                         enabled keys' routing groups.
  --model <id>           hermes only: pin the upstream model, skipping the
                         picker. Omit on a TTY to choose from your model
                         catalog. claude/codex/gemini set their own model —
                         pass model flags to them after -- instead.
  --sanitize             Opt in to the local sanitizer proxy (masks detected secrets before they reach the gateway). Off by default — the mask/restore step corrupts coding-agent sessions; for non-agentic SDK traffic use the standalone 'everyapi proxy' instead.
  --                     End of everyapi's option parsing; remaining args are
                         forwarded verbatim to the tool's argv.

Yolo: when launched on a TTY without a pre-passed dangerous flag,
everyapi asks once whether to enable the tool's "skip every safety
prompt" mode (default Yes). Pre-pass the flag with ` + "`-- <flag>`" + ` or
answer "n" to keep the tool's native prompts intact.

EXAMPLES
  everyapi use claude
  everyapi use codex --channel byteplus
  everyapi use claude -- --model opus
  everyapi use glm                     (Claude Code; pick a GLM model)
  everyapi use kimi --model kimi-k2.5  (Claude Code on Kimi, skip the picker)
  everyapi use hermes                  (pick a model interactively)
  everyapi use hermes --model gpt-5.1  (skip the picker)
`

// Use is the buyer onboarding bridge: verify credentials, configure
// the tool's env vars to point at EveryAPI, exec into the tool.
//
// Usage:
//
//	everyapi use claude
//	everyapi use codex
//	everyapi use gemini
//	everyapi use            (no arg → interactive picker over installed tools)
//	everyapi use claude --group byteplus   (relay through the key bound to
//	                              the "byteplus" group instead of the
//	                              newest enabled key; --channel is an
//	                              alias for --group)
//	everyapi use claude --channel  (bare --group/--channel, no value →
//	                              interactive picker over the routing
//	                              groups your enabled keys are bound to)
//	everyapi use claude --sanitize (opt in to the local sanitizer proxy,
//	                              which is off by default)
//	everyapi use claude -- --dangerously-skip-permissions
//	                              (everything after `--` is forwarded
//	                              verbatim to the tool's argv)
//
// Flags may appear before or after the tool name; a value attached
// with `=` (`--channel=byteplus`) is always explicit. Space form
// (`--channel team-a`) consumes the next token as the value unless
// it's another flag or a known tool name — so `everyapi use claude
// --channel` opens the picker while `--channel team-a claude` is
// explicit. A group literally named like a tool (claude/codex/gemini/
// hermes/minimax/qwen/deepseek/byteplus/glm/kimi) needs the `=` form
// when it appears before the tool positional. A bare `--` ends
// everyapi's option parsing; everything after is
// forwarded raw to the tool — use it for tool flags like claude's
// `--dangerously-skip-permissions` or codex's `--dangerously-bypass-*`.
func Use(args []string) error {
	if wantsUseHelp(args) {
		cliout.Println(useUsage)
		return nil
	}

	toolName, group, pickGroup, sanitize, extraArgs, model, err := parseUseArgs(args)
	if err != nil {
		return err
	}

	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return err
	}

	if toolName == "" {
		toolName, err = interactivePicker()
		if err != nil {
			return err
		}
	}

	if pickGroup {
		group, err = pickGroupInteractive(creds)
		if err != nil {
			return err
		}
	}

	t, err := tools.Lookup(toolName)
	if err != nil {
		return err
	}

	// --model applies to tools EveryAPI picks a model for: hermes (ModelEnv)
	// and the Claude Code provider presets (ModelOwner, where --model skips
	// the picker). plain claude/codex/gemini default the model in their own
	// CLI — pass tool model flags after `--` for those.
	if model != "" && t.ModelEnv == "" && t.ModelOwner == "" {
		return fmt.Errorf(i18n.T("use.model_unsupported"), t.ExecName, t.ExecName)
	}

	// Preflight: if the tool's binary isn't on PATH, offer to run
	// its installer. This runs BEFORE the relay-key probe + sanitizer
	// boot so a buyer who's never installed the agent doesn't pay
	// for two network round-trips just to hit "not installed" at the
	// very end. Non-interactive callers (CI, piped stdin) get the
	// original ErrToolNotFound — we're not going to start writing
	// to npm's global prefix from a script that didn't ask for it.
	if err := ensureToolInstalled(t); err != nil {
		return err
	}

	// The device-auth access token can't relay (it's a management
	// credential); resolve the account's relay API key instead.
	relayKey, err := resolveRelayKey(creds, group)
	if err != nil {
		if errors.Is(err, errNoRelayKeyForGroup) {
			return fmt.Errorf(
				"no enabled relay API key in group %q on your account. Create an\n"+
					"API key assigned to that group in the EveryAPI dashboard (%s),\n"+
					"then run 'everyapi auth login' again — or drop --group/--channel to use\n"+
					"the default key.",
				group, api.WebOriginFromBase(creds.APIBase))
		}
		if errors.Is(err, errNoRelayKey) {
			return fmt.Errorf(i18n.T("use.no_relay_key_long"), api.WebOriginFromBase(creds.APIBase))
		}
		// A 401 here means the management token expired while resolving the
		// relay key (the uncached path calls ListTokens with it). Surface
		// the actionable "re-login" line instead of the raw "look up relay
		// API key: 401 …" wrap, which doesn't tell the user what to do.
		if api.IsUnauthorized(err) {
			return errors.New(i18n.T("auth.session_expired"))
		}
		return err
	}

	// Confirm the relay key actually works before exec'ing the tool.
	// /v1/models runs the same TokenAuth/ValidateUserToken as the
	// real traffic, so a 401 here means the tool would just loop on
	// "401 Invalid token" (invalid / expired / disabled / out of
	// quota key) with no hint why. Bail with an actionable message.
	// Only a definitive 401 is fatal: non-401 probe errors (network,
	// 5xx, the transient SystemPerformanceCheck gate) are left to the
	// tool's own retry — false-blocking a working setup on a flaky
	// probe is worse than letting it through.
	//
	// Skip it for a preset that has no --model: that path resolves its
	// model through a FATAL /v1/models catalog fetch below (same
	// TokenAuth), so probing here would just be a redundant second
	// round-trip. A preset WITH --model skips the catalog fetch, so it
	// still needs the probe to catch a dead key before launch.
	if t.ModelOwner == "" || model != "" {
		if perr := api.New(creds.APIBase, relayKey).
			ProbeRelayToken(cliout.WithCtx()); perr != nil && api.IsUnauthorized(perr) {
			invalidateCachedKeyOnReject(creds, group)
			return relayKeyRejectedErr(t.ExecName, creds.APIBase)
		}
	}

	// Resolve the upstream model for tools EveryAPI picks for (hermes).
	// Sets t.ModelEnv in this process so the tool's prepareFn reads it
	// when generating its config. Runs after the relay-key probe so we
	// don't prompt for a model only to bail on a dead key, and uses the
	// relay key (not the management token) so the catalog is scoped to
	// what this key/group can actually reach. No-op for
	// claude/codex/gemini (ModelEnv == "").
	if err := resolveToolModel(t, creds, relayKey, model); err != nil {
		return err
	}

	// Claude Code provider presets (`everyapi use glm/kimi/…`) resolve
	// their model here: a picker scoped to the provider's catalog on a
	// TTY, or the --model flag. Done before the sanitizer spawns so we
	// fail fast (bad pick / empty catalog) without leaving a detached
	// proxy behind. The chosen id is injected into the tool env below.
	presetModel := ""
	if t.ModelOwner != "" {
		presetModel, err = resolveClaudePresetModel(t, creds, relayKey, model, group)
		if err != nil {
			return err
		}
	}

	// Sanitizer integration. Off by default — opt in with --sanitize.
	// When on, the proxy intercepts the tool's traffic, masks sensitive
	// substrings before they reach the gateway, and restores them on the
	// way back. It stays off by default because the mask/restore step
	// corrupts coding-agent sessions: the model writes placeholders into
	// code/files that escape the HTTP round-trip and leak into the
	// transcript. 'everyapi proxy' is the home for non-agentic SDK traffic.
	//
	// The proxy runs IN THIS PROCESS (a goroutine on an ephemeral
	// loopback port), and `everyapi use` stays alive as the tool's
	// parent (see tools.Exec). So the proxy's lifetime is exactly the
	// tool's lifetime — no detached daemon, no pid file, no shared
	// instance, and therefore none of the cross-session orphaning that
	// a shared 127.0.0.1:8888 proxy suffered (kill the session that
	// spawned it and every other session got connection-refused).
	apiBaseForEnv := creds.APIBase
	if sanitize {
		proxyAddr, stop, perr := startInProcessSanitizer(creds.APIBase)
		if perr != nil {
			cliout.Printf(i18n.T("use.sanitizer_warn"), perr)
			cliout.Printf(i18n.T("use.fallback_direct"), creds.APIBase)
			cliout.Printf("%s", i18n.T("use.fallback_hint"))
		} else {
			apiBaseForEnv = proxyAddr
			// Tear the in-process proxy down if Use returns BEFORE handing
			// off to tools.Exec — a yolo-prompt cancel/EOF, a Prepare
			// error, etc. (defers are function-scoped, so this fires on
			// any such return). On the happy path tools.Exec os.Exits,
			// which skips defers, so the proxy lives for the tool's life.
			defer stop()
		}
	}

	env := t.Env(apiBaseForEnv, relayKey)

	// Pin the provider preset's chosen model. Injected into the env map
	// (not os.Setenv) so it overrides any ANTHROPIC_MODEL the user has
	// exported globally — mergeEnv lets the map win over os.Environ.
	if presetModel != "" {
		tools.SetClaudeModel(env, presetModel)
	}

	// Dangerous-mode prompt. Each tool exposes a single "skip
	// every confirmation" switch — an argv flag (Tool.YoloFlag,
	// claude/codex/gemini) or an env var (Tool.YoloEnv, hermes'
	// HERMES_YOLO_MODE). If the user hasn't already passed the flag
	// via `-- <flags>`, offer it through a TTY confirm so they don't
	// have to remember the exact string. Default is YES — `everyapi
	// use` is meant to be the "just run the agent" shortcut, so the
	// press-Enter happy path keeps you out of the per-tool permission
	// loop. Pick "no" once if you want the prompts back.
	yoloAlreadyPassed := t.YoloFlag != "" && containsFlag(extraArgs, t.YoloFlag)
	if (t.YoloFlag != "" || t.YoloEnv != "") && !yoloAlreadyPassed && cliprompt.IsInteractive() {
		enable, perr := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			fmt.Sprintf(i18n.T("use.yolo_prompt"), t.YoloLabel),
			true,
		)
		if perr != nil {
			// Esc / Ctrl-C in the prompt → propagate the cancel
			// sentinel so the launcher loop catches it. EOF on a
			// piped stdin (no answer) defaults to "no, don't enable".
			if errors.Is(perr, cliprompt.ErrPickCancelled) {
				return perr
			}
			if !errors.Is(perr, io.EOF) {
				return perr
			}
		}
		if enable {
			if t.YoloFlag != "" {
				// Prepend so a user-passed flag after `--` still
				// wins on conflict (last-flag wins in Go's flag and
				// in claude/codex/gemini's argv parsing alike).
				extraArgs = append([]string{t.YoloFlag}, extraArgs...)
			}
			if t.YoloEnv != "" {
				// Set before Prepare()'s overlay, which only adds
				// HERMES_HOME and won't clobber this.
				env[t.YoloEnv] = "1"
			}
		} else if t.YoloEnv != "" {
			// Declined. An env-var tool (hermes) may already have the yolo
			// var exported in the parent environment; mergeEnv passes every
			// ambient var through unless we override it, so without this the
			// pre-exported HERMES_YOLO_MODE=1 would reach the child and yolo
			// would stay ON — the opposite of the user's choice. Force it
			// empty (off under any value-based parse: == "1", != "", ParseBool).
			// Flag tools need no counterpart — their bypass is only ever
			// added to argv on yes.
			env[t.YoloEnv] = ""
		}
	}

	// Per-tool pre-exec setup — currently only codex uses this
	// (writes an isolated CODEX_HOME with apikey-mode auth.json +
	// everyapi-provider config.toml, since codex routes via config
	// not env vars and defaults to ChatGPT device-login). Runs
	// AFTER the yolo prompt so a user who Escs out doesn't pay for
	// a file write that's about to be orphaned. Returned env
	// overlays t.Env so the hook can pin CODEX_HOME.
	extraEnv, err := t.Prepare(apiBaseForEnv, relayKey)
	if err != nil {
		return fmt.Errorf("prepare %s: %w", t.ExecName, err)
	}
	for k, v := range extraEnv {
		env[k] = v
	}

	// Surface the resolved base URL so an aspiring debugger knows
	// where the requests are heading. One line, just before we hand the
	// terminal over to the tool.
	if apiBaseForEnv != creds.APIBase {
		cliout.Printf(i18n.T("use.launching_via")+"\n", t.ExecName, apiBaseForEnv, creds.APIBase)
	} else {
		cliout.Printf(i18n.T("use.launching")+"\n", t.ExecName, creds.APIBase)
	}
	// Discard any terminal control-sequence reply (e.g. the OSC 11
	// background-color report a huh picker triggered) still buffered on
	// stdin, so it doesn't leak into the launched tool as phantom input.
	cliprompt.DrainStdin()
	return tools.Exec(t, env, extraArgs)
}

// containsFlag reports whether the user already passed `flag` in
// `--`-trailing args. Matches both `--foo` and `--foo=value` forms.
// Cheap linear scan — extraArgs is at most a handful of tokens.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// wantsUseHelp reports whether argv asks for `everyapi use` help.
//
// Stops at `--` so `everyapi use claude -- --help` still forwards
// --help to the underlying tool. Skips the value token after a
// space-form --group/--channel so a routing group literally named
// "help" isn't hijacked as a usage request. Bare `help` only counts
// as a help command before the tool positional appears — after that
// it's just a stray arg and `parseUseArgs` will surface the real
// "two positionals" error.
func wantsUseHelp(args []string) bool {
	toolSeen := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return false
		}
		if a == "--help" || a == "-h" {
			return true
		}
		if a == "--group" || a == "-group" || a == "--channel" || a == "-channel" || a == "--model" || a == "-model" {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		if a == "help" && !toolSeen {
			return true
		}
		if !strings.HasPrefix(a, "-") {
			toolSeen = true
		}
	}
	return false
}

// startInProcessSanitizer launches the privacy sanitizer proxy as a
// goroutine in THIS process and returns the loopback URL the tool should
// use as its API base. Because the proxy shares our process and
// `everyapi use` stays alive as the tool's parent (see tools.Exec), the
// proxy lives for exactly the tool's lifetime: when the tool exits,
// tools.Exec calls os.Exit and the goroutine — and its listener — go
// with it. No detached daemon, no pid file, no shared instance, so the
// whole cross-session orphaning class is gone.
//
// Returns an error (caller then falls back to launching directly against
// the gateway) if the loopback listener can't be bound, the detector
// config won't load, the proxy can't be constructed, or it doesn't come
// up healthy in time — a launch with no privacy filter beats no launch
// at all. Every failure path releases the listener and log handle so a
// fall-back-to-direct session leaks neither.
//
// On success it also returns a stop func the caller MUST arrange to run
// if it abandons the launch (see the call site's defer) — that's what
// releases the goroutine, the bound port, and the log fd on a
// fall-back-to-direct or an early return before tools.Exec.
func startInProcessSanitizer(upstream string) (string, func(), error) {
	// Bind the loopback listener OURSELVES and hold it. Owning the port
	// end-to-end (rather than picking a free port, closing it, and letting
	// the server re-bind) closes the TOCTOU window: no other process can
	// grab the port in between, so the readiness probe below can only ever
	// reach our own proxy — never a foreign sanitizer that would silently
	// route this session's traffic (and relay key) through another
	// account.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("bind loopback listener: %w", err)
	}
	listen := ln.Addr().String()

	fc, err := sanitizer.LoadFileConfig()
	if err != nil {
		_ = ln.Close()
		return "", nil, fmt.Errorf("load sanitizer config: %w", err)
	}
	logger, closeLog := sanitizerLogger()
	// Construct synchronously so a bad config (e.g. a non-loopback /
	// malformed upstream) surfaces to the caller as a real error instead
	// of vanishing into the background goroutine's log file.
	srv, err := sanitizer.New(sanitizer.Config{
		Listen:       listen,
		UpstreamBase: upstream,
		Detectors:    fc.BuildDetectors(),
		Logger:       logger,
	})
	if err != nil {
		_ = ln.Close()
		closeLog()
		return "", nil, fmt.Errorf("construct sanitizer: %w", err)
	}

	// Serve on the listener we own, for the life of this process.
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(context.Background(), ln) // owns ln; closes it on return
	}()
	// stop unwinds everything: close the listener to make Serve return,
	// wait for the goroutine, then close the log fd. Idempotent enough for
	// our use (a second ln.Close is a harmless error). The happy path
	// never calls it — tools.Exec os.Exits and the goroutine/listener/fd
	// are reclaimed by process exit.
	stop := func() {
		_ = ln.Close()
		<-served
		closeLog()
	}

	// Hand the URL over only once the proxy is actually serving, so the
	// tool doesn't race its first request into a connection-refused.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sanitizerHealthy(listen) {
			return "http://" + listen, stop, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Didn't come up in time: tear down (otherwise a fall-back-to-direct
	// session would leak the goroutine, the bound port, and the fd for its
	// whole, possibly hours-long, duration).
	stop()
	return "", nil, fmt.Errorf("sanitizer did not become healthy on %s within 2s", listen)
}

// sanitizerLogger returns a logger writing to
// ~/.config/everyapi/sanitizer.log (appended) — the same place the
// standalone `everyapi proxy` writes, so proxy events stay in one spot
// for the diagnose tooling — plus a closer for the underlying file. It
// MUST NOT log to stderr: that stream is shared with the interactive
// tool's TUI and would corrupt the display. Falls back to discarding
// (and a no-op closer) on any error.
//
// The caller closes the file on a startup-failure path; on success the
// handle is owned by the long-lived proxy goroutine and reclaimed when
// the process exits.
func sanitizerLogger() (*log.Logger, func()) {
	discard := func() (*log.Logger, func()) { return log.New(io.Discard, "", 0), func() {} }
	dir, err := config.ConfigDir()
	if err != nil {
		return discard()
	}
	dir = strings.TrimRight(dir, "/")
	// Create the config dir if this is the first command to need it —
	// otherwise a fresh install (where ~/.config/everyapi doesn't exist
	// yet) drops every proxy log line on the floor.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return discard()
	}
	f, err := os.OpenFile(dir+"/sanitizer.log",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return discard()
	}
	return log.New(f, "", log.LstdFlags), func() { _ = f.Close() }
}

// sanitizerHealthy is a 250ms probe to /__sanitizer/health. Returns
// true only on a 200 response — anything else (network error, 404
// from some unrelated service occupying the port, 5xx) is treated as
// "not our proxy".
func sanitizerHealthy(listen string) bool {
	client := &http.Client{Timeout: 250 * time.Millisecond}
	resp, err := client.Get("http://" + listen + "/__sanitizer/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16))
	return strings.TrimSpace(string(body)) == "ok"
}

// parseUseArgs splits `everyapi use` argv into the tool name, the
// optional routing-group selector, and any args meant for the
// underlying tool. Hand-rolled instead of stdlib flag because flag
// stops at the first positional (so `everyapi use claude --channel`
// would never see the flag) and can't express "flag present but
// valueless" (the picker trigger).
//
// --group / --channel (and single-dash forms) are aliases. `=value` is
// an explicit value; an empty `=` or a bare flag opens the picker.
// Space form consumes the next token as the value unless it's another
// flag or a known tool name we still need.
//
// A bare `--` is the standard Unix end-of-options marker: every
// remaining token is appended to extraArgs verbatim and forwarded
// to the tool. Without `--`, unknown flags are an error — that's the
// typo-catching surface we don't want to give up just to spare users
// two characters when they want to pass `--dangerously-skip-permissions`.
func parseUseArgs(args []string) (toolName, group string, pickGroup, sanitize bool, extraArgs []string, model string, err error) {
	knownTool := func(s string) bool { _, e := tools.Lookup(s); return e == nil }

	var positional []string
	groupSeen := false
	groupHasVal := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// End of everyapi's option parsing — forward the rest raw.
			if i+1 < len(args) {
				extraArgs = append(extraArgs, args[i+1:]...)
			}
			break
		}
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		val := ""
		hasEq := false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			val, name, hasEq = name[eq+1:], name[:eq], true
		}
		switch name {
		case "sanitize":
			// Opt in to the local sanitizer proxy (masks detected
			// secrets before they reach the gateway). Off by default —
			// the mask/restore step leaks placeholders into coding-agent
			// sessions and corrupts them; non-agentic SDK traffic should
			// use the standalone 'everyapi proxy' instead.
			//
			// Honor an attached value so the standard `-flag=false`
			// bool convention works (`--sanitize=false` must DISABLE,
			// not silently enable). A bare `--sanitize` opts in.
			if hasEq {
				b, err := strconv.ParseBool(val)
				if err != nil {
					return "", "", false, false, nil, "", fmt.Errorf(i18n.T("use.sanitize_bad_value"), val)
				}
				sanitize = b
				continue
			}
			sanitize = true
		case "model":
			// Pin the upstream model for model-selectable tools
			// (hermes), skipping the interactive picker. Validated
			// against the tool's capability after Lookup. `=value`
			// or space form; a bare/empty --model is an error since
			// "no value" already has a meaning (the picker) reached
			// by simply omitting the flag. The space form won't eat a
			// not-yet-seen tool name (`--model hermes`) — that token
			// is the tool, leaving --model dangling (the error path).
			if hasEq {
				if val == "" {
					return "", "", false, false, nil, "", errors.New(i18n.T("use.model_needs_value"))
				}
				model = val
				continue
			}
			if i+1 < len(args) {
				nx := args[i+1]
				if !strings.HasPrefix(nx, "-") && !(knownTool(nx) && len(positional) == 0) {
					model = nx
					i++
					continue
				}
			}
			return "", "", false, false, nil, "", errors.New(i18n.T("use.model_needs_value"))
		case "group", "channel":
			groupSeen = true
			if hasEq {
				if val != "" {
					group, groupHasVal = val, true
				}
				continue
			}
			if i+1 < len(args) {
				nx := args[i+1]
				if !strings.HasPrefix(nx, "-") && !(knownTool(nx) && len(positional) == 0) {
					group, groupHasVal = nx, true
					i++
				}
			}
		default:
			return "", "", false, false, nil, "", fmt.Errorf(i18n.T("use.unknown_flag"), a, a)
		}
	}

	if len(positional) > 1 {
		return "", "", false, false, nil, "", errors.New(i18n.T("use.usage"))
	}
	if len(positional) == 1 {
		toolName = positional[0]
	}
	pickGroup = groupSeen && !groupHasVal
	return toolName, group, pickGroup, sanitize, extraArgs, model, nil
}

// resolveToolModel pins the upstream model for tools EveryAPI selects a
// model for (those with Tool.ModelEnv set — hermes today). It exports
// the resolved id into t.ModelEnv in THIS process so the tool's
// prepareFn reads it when generating config. Precedence:
//
//  1. --model <id> (explicit flag) → use it, no prompt.
//  2. t.ModelEnv already set in the environment → respect it, no prompt.
//  3. interactive TTY → model picker over the gateway catalog.
//  4. non-interactive with nothing set → no-op; prepareFn falls back to
//     its built-in default (so scripts/CI still launch).
//
// A no-op for claude/codex/gemini, whose CLIs default the model
// themselves and route it by name through the gateway.
func resolveToolModel(t *tools.Tool, creds *config.Credentials, relayKey, modelFlag string) error {
	if t.ModelEnv == "" {
		return nil
	}
	if modelFlag != "" {
		return os.Setenv(t.ModelEnv, modelFlag)
	}
	if os.Getenv(t.ModelEnv) != "" {
		return nil // explicit env override; respect it
	}
	if !cliprompt.IsInteractive() {
		return nil // let prepareFn use its built-in default
	}
	chosen, err := pickModelInteractive(t, creds, relayKey)
	if err != nil {
		return err
	}
	if chosen != "" {
		return os.Setenv(t.ModelEnv, chosen)
	}
	return nil
}

// relayKeyRejectedErr is the actionable message shown when EveryAPI
// 401s a relay key. Shared by the pre-launch probe and the preset
// catalog fetch (both hit /v1/models with the same TokenAuth), so a
// dead key gives identical "check / top up / refresh" guidance wherever
// it's first noticed.
func relayKeyRejectedErr(execName, apiBase string) error {
	wallet := api.WebOriginFromBase(apiBase) + "/wallet"
	return fmt.Errorf(
		"EveryAPI rejected the relay API key — not launching %s, it would just\n"+
			"loop on 401. The key is invalid, expired, disabled, or out of quota.\n"+
			"  check:    everyapi auth status\n"+
			"  top up:   %s\n"+
			"  refresh:  everyapi auth login",
		execName, wallet)
}

// invalidateCachedKeyOnReject clears the cached default-group relay key
// after the pre-launch relay check came back a definitive 401, so the
// next `everyapi use` re-resolves to a live sibling token instead of
// re-handing-out the dead cached key (the bug this guards: a token
// disabled/revoked/expired server-side leaves the default-group cache
// pointing at a key the gateway now rejects, with nothing to teach it
// otherwise).
//
// Gated on group == "": only the default-group path caches its key. A
// --group/--channel key is resolved fresh and never cached, so its
// rejection must NOT wipe the unrelated (possibly still-valid) default
// cache. The on-disk clear is best-effort — the user is being told to
// re-login/refresh anyway (which rewrites credentials.json) — so a Save
// failure is surfaced as a warning rather than masking the real
// "key rejected" error we're about to return.
func invalidateCachedKeyOnReject(creds *config.Credentials, group string) {
	if group != "" {
		return
	}
	if err := api.InvalidateCachedRelayKey(creds); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not clear the rejected relay key from credentials.json:", err)
	}
}

// resolveClaudePresetModel picks the upstream model for a Claude Code
// provider preset (Tool.ModelOwner set — glm/kimi/minimax/…). Precedence:
//
//  1. --model <id> (explicit flag) → use it, no picker.
//  2. interactive TTY → picker over the provider's models, filtered from
//     the live gateway catalog by the model's `owned_by`.
//  3. non-interactive with no --model → error: a script must name the
//     model so we never silently pick on the user's behalf.
//
// Unlike resolveToolModel (hermes), an empty/failed catalog is FATAL
// here: the whole point of `everyapi use glm` is to run a glm model, so
// silently falling back to Claude Code's default Anthropic model would
// be a worse surprise than a clear "no glm models reachable" error.
func resolveClaudePresetModel(t *tools.Tool, creds *config.Credentials, relayKey, modelFlag, group string) (string, error) {
	if modelFlag != "" {
		return modelFlag, nil
	}
	if !cliprompt.IsInteractive() {
		return "", fmt.Errorf(i18n.T("use.preset_needs_model"), t.Name)
	}
	catalog, err := api.New(creds.APIBase, relayKey).RelayModelCatalog(cliout.WithCtx())
	if err != nil {
		// This fetch doubles as the relay-key probe for presets (Use
		// skips the standalone probe when we'll reach here), so a 401
		// gets the same actionable "dead key" guidance — and, when the
		// rejected key was the default-group cache, clears it so the
		// next run re-resolves to a live token instead of re-handing-out
		// the dead one.
		if api.IsUnauthorized(err) {
			invalidateCachedKeyOnReject(creds, group)
			return "", relayKeyRejectedErr(t.ExecName, creds.APIBase)
		}
		return "", fmt.Errorf(i18n.T("use.preset_catalog_failed"), t.Name, err)
	}
	ids := providerChatModels(catalog, t.ModelOwner)
	if len(ids) == 0 {
		return "", fmt.Errorf(i18n.T("use.preset_no_models"),
			t.Name, api.WebOriginFromBase(creds.APIBase))
	}
	idx, err := cliprompt.Pick(fmt.Sprintf(i18n.T("use.model_picker"), t.Name), ids)
	if err != nil {
		return "", err
	}
	return ids[idx], nil
}

// chatCapableEndpoints are the GET /v1/models `supported_endpoint_types`
// a Claude Code preset can actually drive — text chat over the
// OpenAI/Anthropic/Gemini wire. Image, embeddings, rerank and video
// models share a provider's owned_by (MiniMax's speech-02/image-01, for
// one) but Claude Code can't use them, so the preset picker hides them.
var chatCapableEndpoints = map[string]bool{
	"openai":                  true,
	"openai-response":         true,
	"openai-response-compact": true,
	"anthropic":               true,
	"gemini":                  true,
}

// legacyOwnerAliases tolerates gateways that haven't shipped the
// owned_by de-channelization. A preset owner is the model BRAND the
// gateway now reports (e.g. "zhipu", "qwen"); an older gateway instead
// reports the channel-adaptor name ("zhipu_4v", "ali"). Accepting both
// lets `use glm`/`use qwen` work whichever the gateway emits, so the CLI
// can roll out ahead of the backend. Remove once every gateway reports
// brands.
var legacyOwnerAliases = map[string][]string{
	"zhipu": {"zhipu_4v"},
	"qwen":  {"ali"},
}

// ownerMatches reports whether a model's owned_by belongs to the preset
// owner — the brand slug itself or any legacy channel-name alias.
func ownerMatches(ownedBy, owner string) bool {
	if strings.EqualFold(ownedBy, owner) {
		return true
	}
	for _, alias := range legacyOwnerAliases[owner] {
		if strings.EqualFold(ownedBy, alias) {
			return true
		}
	}
	return false
}

// providerChatModels filters a relay catalog down to the chat-capable
// model ids owned by `owner` (brand slug, case-insensitive; legacy
// channel names tolerated via ownerMatches), sorted. A model that
// declares no endpoint types is kept (fail-open) so missing metadata
// never hides an otherwise-valid chat model — only a model that
// explicitly serves ONLY non-chat endpoints is dropped.
func providerChatModels(catalog []api.RelayModel, owner string) []string {
	var ids []string
	for _, m := range catalog {
		if !ownerMatches(m.OwnedBy, owner) {
			continue
		}
		if !chatCapable(m.SupportedEndpointTypes) {
			continue
		}
		ids = append(ids, cliout.Sanitize(m.ID))
	}
	sort.Strings(ids)
	return ids
}

// chatCapable reports whether a model serving these endpoint types can
// be driven as a Claude Code chat model. Empty/unknown → true
// (fail-open): the gateway populates the field, so empty means "not
// reported", and hiding a model on missing metadata is worse than
// showing one extra.
func chatCapable(types []string) bool {
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		if chatCapableEndpoints[t] {
			return true
		}
	}
	return false
}

// pickModelInteractive lists the models the relay key can route to
// (GET /v1/models with the relay key — group-scoped, so the picker only
// offers models the launched tool will really reach) and asks the user
// to pick one. The cursor defaults to the model pinned on the last
// launch when it's still offered. A catalog-fetch failure or an empty
// catalog is non-fatal: it returns "" so the launch proceeds on the
// tool's built-in default rather than blocking.
func pickModelInteractive(t *tools.Tool, creds *config.Credentials, relayKey string) (string, error) {
	models, err := api.New(creds.APIBase, relayKey).RelayModels(cliout.WithCtx())
	if err != nil {
		cliout.Printf(i18n.T("use.model_fetch_failed")+"\n", err, t.ExecName)
		return "", nil
	}
	if len(models) == 0 {
		cliout.Printf(i18n.T("use.model_none")+"\n", t.ExecName)
		return "", nil
	}
	// Server-supplied model IDs render directly in the interactive picker
	// below; strip any embedded ANSI/control sequences before display,
	// matching cliout.Sanitize's use elsewhere in the CLI for backend-
	// relayed strings (model names, channel names, ...).
	for i, m := range models {
		models[i] = cliout.Sanitize(m)
	}
	sort.Strings(models)
	// Default the cursor to last launch's model when it's still in the
	// catalog. LastHermesModel is hermes-specific, which is fine while
	// hermes is the only ModelEnv tool; generalize if that changes.
	initial := 0
	if last := tools.LastHermesModel(); last != "" {
		for i, m := range models {
			if m == last {
				initial = i
				break
			}
		}
	}
	idx, err := cliprompt.PickWithSelected(
		fmt.Sprintf(i18n.T("use.model_picker"), t.ExecName),
		models, initial)
	if err != nil {
		return "", err
	}
	return models[idx], nil
}

// pickGroupInteractive lists the distinct routing groups the account's
// ENABLED relay tokens are bound to and asks the user to pick one. The
// buyer CLI has no channel-listing endpoint (that's admin-only), so
// "available channels" is necessarily expressed as the groups the user
// already holds a key for. The empty group (default tokens) shows as
// "(default — newest enabled key)" and selecting it returns "" — the
// normal newest-enabled-key path.
func pickGroupInteractive(creds *config.Credentials) (string, error) {
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	tokens, err := client.ListTokens(cliout.WithCtx())
	if err != nil {
		return "", fmt.Errorf("list tokens for the group picker: %w", err)
	}
	seen := map[string]bool{}
	var groups []string
	for i := range tokens {
		if tokens[i].Status != api.TokenStatusEnabled {
			continue
		}
		if g := tokens[i].Group; !seen[g] {
			seen[g] = true
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return "", errors.New(i18n.T("use.no_relay_keys"))
	}
	labels := make([]string, len(groups))
	for i, g := range groups {
		if g == "" {
			labels[i] = "(default — newest enabled key)"
		} else {
			labels[i] = cliout.Sanitize(g)
		}
	}
	idx, err := cliprompt.Pick(i18n.T("use.group_picker"), labels)
	if err != nil {
		return "", err
	}
	return groups[idx], nil
}

// ensureToolInstalled is the preflight gate between Lookup and the
// network probes. If the tool is already on PATH it's a no-op. If
// it's missing and the session is non-interactive (CI / piped
// stdin), or the tool has no usable auto-installer for this
// platform, the caller sees the original ErrToolNotFound — same
// behavior as before this helper existed. If it's missing AND we
// have a TTY AND CanAutoInstall returns true, we surface a yes/no
// confirm. Default is YES for routine package-manager installs and
// NO for installers that pipe a remote shell script into bash —
// pressing Enter shouldn't ever run untrusted code fetched at
// install time. On Yes we stream the installer's output to the
// terminal so npm / curl progress reaches the user live, then
// return nil so the caller proceeds to launch.
//
// `ErrInstalledButNotOnPath` from RunInstall is translated here so
// the user gets a localized "installed but not on PATH — open a new
// shell" message instead of the raw English error type.
func ensureToolInstalled(t *tools.Tool) error {
	if tools.IsInstalled(t) {
		return nil
	}
	if !cliprompt.IsInteractive() || !tools.CanAutoInstall(t) {
		return &tools.ErrToolNotFound{Tool: t}
	}
	// The auto-installer shells out to a package manager (npm) through a
	// non-interactive shell that doesn't source the user's rc files. If
	// that command isn't resolvable on PATH — the classic case being a
	// version-manager npm exposed only as a shell function — offering the
	// install just yields a cryptic "npm: command not found". Catch it
	// here and tell the user exactly what to install first.
	if missing := tools.InstallerMissing(t); missing != "" {
		return fmt.Errorf(i18n.T("use.installer_missing"), t.ExecName, missing, t.InstallHint)
	}
	cliout.Printf(i18n.T("use.tool_not_installed")+"\n", t.ExecName)
	cliout.Printf("  %s\n", t.InstallCmd)
	ok, err := cliprompt.YesNo(
		bufio.NewReader(os.Stdin),
		fmt.Sprintf(i18n.T("use.install_prompt"), t.Name),
		t.InstallPromptDefault(),
	)
	if err != nil {
		// Esc / Ctrl-C from the confirm propagates as cancellation
		// so the launcher loop catches it. ensureToolInstalled is
		// only reached after IsInteractive() — a TTY won't EOF on
		// read — so we don't carve out an EOF special case here.
		return err
	}
	if !ok {
		return &tools.ErrToolNotFound{Tool: t}
	}
	cliout.Printf(i18n.T("use.installing")+"\n", t.Name)
	if err := tools.RunInstall(t); err != nil {
		var notOnPath *tools.ErrInstalledButNotOnPath
		if errors.As(err, &notOnPath) {
			return fmt.Errorf(i18n.T("use.installed_not_on_path"), notOnPath.Tool.ExecName)
		}
		return err
	}
	cliout.Printf(i18n.T("use.installed")+"\n", t.Name)
	return nil
}

// interactivePicker is the no-arg fallback. Renders the registered
// tools as an arrow-navigable list when run on a TTY; falls back to
// a numbered prompt otherwise (CI / piped input).
func interactivePicker() (string, error) {
	names := tools.Names()
	idx, err := cliprompt.Pick(i18n.T("use.tool_picker"), names)
	if err != nil {
		return "", err
	}
	return names[idx], nil
}
