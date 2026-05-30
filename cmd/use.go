package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
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
)

// useUsage is split so the prose can embed literal backticks (e.g.
// "`-- <flag>`") without colliding with the raw-string delimiter.
const useUsage = `everyapi use — launch a third-party CLI routed through EveryAPI

USAGE
  everyapi use [<tool>] [--group <name> | --channel <name>] [--model <id>] [--direct] [-- tool args...]

ARGUMENTS
  <tool>                 claude | codex | gemini | hermes
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
  --direct               Bypass the local sanitizer proxy (no privacy filter).
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
//	everyapi use claude --direct   (no-op today; flag reserved for the
//	                              future sanitizer proxy bypass)
//	everyapi use claude -- --dangerously-skip-permissions
//	                              (everything after `--` is forwarded
//	                              verbatim to the tool's argv)
//
// Flags may appear before or after the tool name; a value attached
// with `=` (`--channel=byteplus`) is always explicit. Space form
// (`--channel byteplus`) consumes the next token as the value unless
// it's another flag or a known tool name — so `everyapi use claude
// --channel` opens the picker while `--channel byteplus claude` is
// explicit. A group literally named claude/codex/gemini needs the `=`
// form. A bare `--` ends everyapi's option parsing; everything after is
// forwarded raw to the tool — use it for tool flags like claude's
// `--dangerously-skip-permissions` or codex's `--dangerously-bypass-*`.
func Use(args []string) error {
	if wantsUseHelp(args) {
		cliout.Println(useUsage)
		return nil
	}

	toolName, group, pickGroup, direct, extraArgs, model, err := parseUseArgs(args)
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

	// --model only applies to tools EveryAPI picks a model for (hermes).
	// claude/codex/gemini default the model in their own CLI — pass tool
	// model flags after `--` for those.
	if model != "" && t.ModelEnv == "" {
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
					"then run 'everyapi login' again — or drop --group/--channel to use\n"+
					"the default key.",
				group, api.WebOriginFromBase(creds.APIBase))
		}
		if errors.Is(err, errNoRelayKey) {
			return fmt.Errorf(i18n.T("use.no_relay_key_long"), api.WebOriginFromBase(creds.APIBase))
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
	if perr := api.New(creds.APIBase, relayKey).
		ProbeRelayToken(cliout.WithCtx()); perr != nil && api.IsUnauthorized(perr) {
		wallet := api.WebOriginFromBase(creds.APIBase) + "/wallet"
		return fmt.Errorf(
			"EveryAPI rejected the relay API key — not launching %s, it would just\n"+
				"loop on 401. The key is invalid, expired, disabled, or out of quota.\n"+
				"  check:    everyapi status\n"+
				"  top up:   %s\n"+
				"  refresh:  everyapi login",
			t.ExecName, wallet)
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

	// Sanitizer integration. Default is "on" — the proxy intercepts
	// the tool's traffic, masks sensitive substrings before they
	// reach the gateway, and restores them on the way back. The
	// --direct flag bypasses it (the tool talks straight to the
	// gateway, no privacy filter). Auto-start the proxy in detached
	// mode when it isn't already up — `everyapi use` should be a one-
	// command experience, not "remember to run two windows".
	apiBaseForEnv := creds.APIBase
	if !direct {
		proxyAddr, perr := ensureSanitizerRunning(creds.APIBase)
		if perr != nil {
			cliout.Printf(i18n.T("use.sanitizer_warn"), perr)
			cliout.Printf(i18n.T("use.fallback_direct"), creds.APIBase)
			cliout.Printf("%s", i18n.T("use.fallback_hint"))
		} else {
			apiBaseForEnv = proxyAddr
		}
	}

	env := t.Env(apiBaseForEnv, relayKey)

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
	// where the requests are heading. One line, before the exec
	// disappears the parent process.
	if apiBaseForEnv != creds.APIBase {
		cliout.Printf(i18n.T("use.launching_via")+"\n", t.ExecName, apiBaseForEnv, creds.APIBase)
	} else {
		cliout.Printf(i18n.T("use.launching")+"\n", t.ExecName, creds.APIBase)
	}
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
		if a == "--group" || a == "-group" || a == "--channel" || a == "-channel" {
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

// ensureSanitizerRunning checks if a sanitizer proxy is already
// listening on `listen`; if not, spawns one in detached mode pointing
// at the given upstream. Returns the proxy's http URL (e.g.
// "http://127.0.0.1:8888") that the caller should pass as apiBase
// when building the tool's env vars.
//
// The detached proxy is tied to our pid via --parent-pid: when the
// caller (this `everyapi use` invocation, then the tool that inherits
// its pid via exec) exits, the proxy notices within ~2s and shuts
// down. That implements spec §7-1 step 2's "父进程退出时清理" without
// leaving a long-lived background proxy behind.
//
// On any failure the caller is responsible for falling back to direct
// mode — better to launch the tool against the gateway directly than
// to refuse to launch at all.
func ensureSanitizerRunning(upstream string) (string, error) {
	// Pick the listen address. Three-tier resolution, prefer
	// stable / discoverable answers first so a debugger landing
	// on a running session doesn't have to chase ephemeral ports:
	//
	//   1. If sanitizer.pid records a listen and that address is
	//      currently serving our sanitizer, reuse it. Same
	//      process across multiple `use` invocations.
	//   2. Else if 127.0.0.1:8888 is sanitizer-healthy already,
	//      use it (covers the case where the PID file got cleared
	//      but the proxy is still alive).
	//   3. Else pick a fresh listen: 8888 if free, otherwise a
	//      kernel-assigned ephemeral port. The chosen address
	//      gets written into sanitizer.pid by 'proxy start', so
	//      'proxy status' and the next 'use' both find it.
	const defaultListen = "127.0.0.1:8888"
	if listen := sanitizerListenFromPID(); listen != "" && sanitizerHealthy(listen) {
		return "http://" + listen, nil
	}
	if sanitizerHealthy(defaultListen) {
		return "http://" + defaultListen, nil
	}
	listen := defaultListen
	if portOccupied(listen) {
		port, err := pickFreePort()
		if err != nil {
			return "", fmt.Errorf("port %s is held by another process and no free fallback port found: %w", defaultListen, err)
		}
		listen = fmt.Sprintf("127.0.0.1:%d", port)
	}
	url := "http://" + listen
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate self: %w", err)
	}
	cmd := exec.Command(exe, "proxy", "start",
		"--listen", listen,
		"--upstream", upstream,
		"--detach",
		"--parent-pid", strconv.Itoa(os.Getpid()),
	)
	// Forward stderr so the user sees any startup errors.
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("spawn proxy: %w", err)
	}
	return url, nil
}

// pickFreePort asks the kernel for an unused ephemeral port by
// binding 127.0.0.1:0 and reading back what we got. The listener
// closes immediately — there's an inherent TOCTOU window before
// the caller's spawned proxy re-binds, but it's measured in
// microseconds on a typical desktop, vs. seconds for the
// alternative of probing a hardcoded ladder (8889, 8890, …) and
// hoping each one stays free.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// sanitizerListenFromPID reads the listen address recorded by
// 'proxy start' into ~/.config/everyapi/sanitizer.pid. Returns
// the empty string if the file is missing, malformed, or written
// by an old binary that only persisted the PID — callers MUST
// treat empty as "no recorded listen, pick one fresh".
//
// File format (from cmd/proxy/proxy.go writePIDFile):
//
//	"<pid> <listen-addr>\n"   (current)
//	"<pid>\n"                  (legacy)
func sanitizerListenFromPID() string {
	dir, err := config.ConfigDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(strings.TrimRight(dir, "/") + "/sanitizer.pid")
	if err != nil {
		return ""
	}
	var pid int
	var listen string
	n, _ := fmt.Sscanf(strings.TrimSpace(string(data)), "%d %s", &pid, &listen)
	if n < 2 || pid <= 0 {
		return ""
	}
	return listen
}

// portOccupied returns true when SOMETHING accepts a TCP connection
// to `listen` — without saying whether that something is the
// EveryAPI sanitizer or an unrelated service. Use AFTER
// sanitizerHealthy to discriminate "free port" from "someone else's
// port"; the combination tells the caller whether spawning a fresh
// sanitizer on this address can possibly succeed.
//
// 250 ms dial timeout matches sanitizerHealthy's HTTP probe budget:
// we're answering the same "is anyone there" question at the
// transport layer instead of the application layer.
func portOccupied(listen string) bool {
	conn, err := net.DialTimeout("tcp", listen, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
func parseUseArgs(args []string) (toolName, group string, pickGroup, direct bool, extraArgs []string, model string, err error) {
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
		case "direct":
			// Bypass the sanitizer proxy; the tool talks straight to
			// api.everyapi.ai. Use when you're certain the prompt
			// won't carry secrets, or when debugging proxy issues.
			direct = true
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
	return toolName, group, pickGroup, direct, extraArgs, model, nil
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
			labels[i] = g
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
