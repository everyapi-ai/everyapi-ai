package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/everyapi-ai/everyapi-ai/internal/tools"
)

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
	toolName, group, pickGroup, direct, extraArgs, err := parseUseArgs(args)
	if err != nil {
		return err
	}

	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New("not logged in — run 'everyapi login' first")
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
			return fmt.Errorf(
				"no usable relay API key on your account — `everyapi use` needs one,\n"+
					"and it's separate from your login token. Create an API key in the\n"+
					"EveryAPI dashboard (%s), then run 'everyapi login' again.",
				api.WebOriginFromBase(creds.APIBase))
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

	// Sanitizer integration. Default is "on" — the proxy intercepts
	// the tool's traffic, masks sensitive substrings before they
	// reach the gateway, and restores them on the way back. The
	// --direct flag bypasses it (the tool talks straight to the
	// gateway, no privacy filter). Auto-start the proxy in detached
	// mode when it isn't already up — `everyapi use` should be a one-
	// command experience, not "remember to run two windows".
	apiBaseForEnv := creds.APIBase
	if !direct {
		const proxyListen = "127.0.0.1:8888"
		proxyAddr, perr := ensureSanitizerRunning(proxyListen, creds.APIBase)
		if perr != nil {
			cliout.Printf("Warning: sanitizer proxy didn't start (%v).\n", perr)
			cliout.Printf("Falling back to direct mode — your traffic will reach %s without the privacy filter.\n", creds.APIBase)
			cliout.Printf("Re-run with --direct to silence this, or 'everyapi proxy start' to debug.\n\n")
		} else {
			apiBaseForEnv = proxyAddr
		}
	}

	env := t.Env(apiBaseForEnv, relayKey)
	// Surface the resolved base URL so an aspiring debugger knows
	// where the requests are heading. One line, before the exec
	// disappears the parent process.
	if apiBaseForEnv != creds.APIBase {
		cliout.Printf("Launching %s against %s → %s\n", t.ExecName, apiBaseForEnv, creds.APIBase)
	} else {
		cliout.Printf("Launching %s against %s\n", t.ExecName, creds.APIBase)
	}
	return tools.Exec(t, env, extraArgs)
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
func ensureSanitizerRunning(listen, upstream string) (string, error) {
	url := "http://" + listen
	// Fast path: already healthy. The existing proxy keeps whatever
	// parent-pid binding it was started with — we don't retrofit
	// ours; the cost of "previous parent dies, proxy outlives it
	// until idle-replaced" is bounded by the existing detector's 2s
	// poll.
	if sanitizerHealthy(listen) {
		return url, nil
	}
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
func parseUseArgs(args []string) (toolName, group string, pickGroup, direct bool, extraArgs []string, err error) {
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
			return "", "", false, false, nil, fmt.Errorf("unknown flag %q (use `--` before tool flags: `everyapi use <tool> -- %s ...`)", a, a)
		}
	}

	if len(positional) > 1 {
		return "", "", false, false, nil, fmt.Errorf("usage: everyapi use <tool> [--group <name>|--channel <name>] [--direct] [-- tool args...]")
	}
	if len(positional) == 1 {
		toolName = positional[0]
	}
	pickGroup = groupSeen && !groupHasVal
	return toolName, group, pickGroup, direct, extraArgs, nil
}

// pickGroupInteractive lists the distinct routing groups the account's
// ENABLED relay tokens are bound to and asks the user to pick one. The
// buyer CLI has no channel-listing endpoint (that's admin-only), so
// "available channels" is necessarily expressed as the groups the user
// already holds a key for. The empty group (default tokens) shows as
// "(default)" and selecting it returns "" — the normal newest-enabled
// -key path.
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
		return "", errors.New("no enabled relay API keys on your account to pick a group from — create one in the EveryAPI dashboard, then 'everyapi login'")
	}
	cliout.Println("Pick a routing group:")
	for i, g := range groups {
		label := g
		if g == "" {
			label = "(default — newest enabled key)"
		}
		cliout.Printf("  %d) %s\n", i+1, label)
	}
	cliout.Printf("Enter name or number: ")
	var choice string
	if _, err := fmt.Scanln(&choice); err != nil {
		return "", fmt.Errorf("read selection: %w", err)
	}
	choice = strings.TrimSpace(choice)
	for i, g := range groups {
		if choice == fmt.Sprintf("%d", i+1) || choice == g {
			return g, nil
		}
		if g == "" && strings.EqualFold(choice, "default") {
			return "", nil
		}
	}
	return "", fmt.Errorf("unknown selection %q", choice)
}

// interactivePicker is the no-arg fallback. Stays simple: list the
// registered tools, ask the user to pick by name. Avoids dragging in
// a TUI library — `everyapi use` with an arg is the primary path.
func interactivePicker() (string, error) {
	names := tools.Names()
	cliout.Println("Pick a tool to launch:")
	for i, n := range names {
		cliout.Printf("  %d) %s\n", i+1, n)
	}
	cliout.Printf("Enter name or number: ")
	var choice string
	if _, err := fmt.Scanln(&choice); err != nil {
		return "", fmt.Errorf("read selection: %w", err)
	}
	choice = strings.TrimSpace(choice)
	for i, n := range names {
		if choice == n || choice == fmt.Sprintf("%d", i+1) {
			return n, nil
		}
	}
	return "", fmt.Errorf("unknown selection %q", choice)
}
