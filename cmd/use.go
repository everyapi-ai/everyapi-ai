package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/relaya-ai/relaya-ai/internal/api"
	"github.com/relaya-ai/relaya-ai/internal/config"
	"github.com/relaya-ai/relaya-ai/internal/tools"
)

// Use is the buyer onboarding bridge: verify credentials, configure
// the tool's env vars to point at Relaya, exec into the tool. See
// docs/cli/channel-marketplace.md §7-1 "Onboarding bridge".
//
// Usage:
//
//	relaya use claude
//	relaya use codex
//	relaya use gemini
//	relaya use            (no arg → interactive picker over installed tools)
//	relaya use claude --group byteplus   (relay through the key bound to
//	                              the "byteplus" group instead of the
//	                              newest enabled key; --channel is an
//	                              alias for --group)
//	relaya use claude --channel  (bare --group/--channel, no value →
//	                              interactive picker over the routing
//	                              groups your enabled keys are bound to)
//	relaya use claude --direct   (no-op today; flag reserved for the
//	                              future sanitizer proxy bypass)
//	relaya use claude -- --dangerously-skip-permissions
//	                              (everything after `--` is forwarded
//	                              verbatim to the tool's argv)
//
// Flags may appear before or after the tool name; a value attached
// with `=` (`--channel=byteplus`) is always explicit. Space form
// (`--channel byteplus`) consumes the next token as the value unless
// it's another flag or a known tool name — so `relaya use claude
// --channel` opens the picker while `--channel byteplus claude` is
// explicit. A group literally named claude/codex/gemini needs the `=`
// form. A bare `--` ends relaya's option parsing; everything after is
// forwarded raw to the tool — use it for tool flags like claude's
// `--dangerously-skip-permissions` or codex's `--dangerously-bypass-*`.
func Use(args []string) error {
	toolName, group, pickGroup, extraArgs, err := parseUseArgs(args)
	if err != nil {
		return err
	}

	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New("not logged in — run 'relaya login' first")
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
					"API key assigned to that group in the Relaya dashboard (%s),\n"+
					"then run 'relaya login' again — or drop --group/--channel to use\n"+
					"the default key.",
				group, trimAPIBaseToWebOrigin(creds.APIBase))
		}
		if errors.Is(err, errNoRelayKey) {
			return fmt.Errorf(
				"no usable relay API key on your account — `relaya use` needs one,\n"+
					"and it's separate from your login token. Create an API key in the\n"+
					"Relaya dashboard (%s), then run 'relaya login' again.",
				trimAPIBaseToWebOrigin(creds.APIBase))
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
		ProbeRelayToken(withCtx()); perr != nil && api.IsUnauthorized(perr) {
		wallet := trimAPIBaseToWebOrigin(creds.APIBase) + "/wallet"
		return fmt.Errorf(
			"Relaya rejected the relay API key — not launching %s, it would just\n"+
				"loop on 401. The key is invalid, expired, disabled, or out of quota.\n"+
				"  check:    relaya status\n"+
				"  top up:   %s\n"+
				"  refresh:  relaya login",
			t.ExecName, wallet)
	}

	env := t.Env(creds.APIBase, relayKey)
	// Surface the resolved base URL so an aspiring debugger knows
	// where the requests are heading. One line, before the exec
	// disappears the parent process.
	printf("Launching %s against %s\n", t.ExecName, creds.APIBase)
	return tools.Exec(t, env, extraArgs)
}

// parseUseArgs splits `relaya use` argv into the tool name, the
// optional routing-group selector, and any args meant for the
// underlying tool. Hand-rolled instead of stdlib flag because flag
// stops at the first positional (so `relaya use claude --channel`
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
func parseUseArgs(args []string) (toolName, group string, pickGroup bool, extraArgs []string, err error) {
	knownTool := func(s string) bool { _, e := tools.Lookup(s); return e == nil }

	var positional []string
	groupSeen := false
	groupHasVal := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// End of relaya's option parsing — forward the rest raw.
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
			// reserved future sanitizer-proxy bypass; accepted, ignored.
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
			return "", "", false, nil, fmt.Errorf("unknown flag %q (use `--` before tool flags: `relaya use <tool> -- %s ...`)", a, a)
		}
	}

	if len(positional) > 1 {
		return "", "", false, nil, fmt.Errorf("usage: relaya use <tool> [--group <name>|--channel <name>] [-- tool args...]")
	}
	if len(positional) == 1 {
		toolName = positional[0]
	}
	pickGroup = groupSeen && !groupHasVal
	return toolName, group, pickGroup, extraArgs, nil
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
	tokens, err := client.ListTokens(withCtx())
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
		return "", errors.New("no enabled relay API keys on your account to pick a group from — create one in the Relaya dashboard, then 'relaya login'")
	}
	println("Pick a routing group:")
	for i, g := range groups {
		label := g
		if g == "" {
			label = "(default — newest enabled key)"
		}
		printf("  %d) %s\n", i+1, label)
	}
	printf("Enter name or number: ")
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
// a TUI library — `relaya use` with an arg is the primary path.
func interactivePicker() (string, error) {
	names := tools.Names()
	println("Pick a tool to launch:")
	for i, n := range names {
		printf("  %d) %s\n", i+1, n)
	}
	printf("Enter name or number: ")
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
