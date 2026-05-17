package cmd

import (
	"errors"
	"flag"
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
//	relaya use claude --direct   (no-op today; flag reserved for the
//	                              future sanitizer proxy bypass)
func Use(args []string) error {
	fs := flag.NewFlagSet("use", flag.ContinueOnError)
	// --direct is accepted but ignored today. It's the documented
	// future bypass for the sanitizer proxy (channel-marketplace.md
	// §7-1). Accepting it now means a user (or doc) that wires
	// --direct doesn't break when the proxy ships later.
	_ = fs.Bool("direct", false, "reserved: bypass sanitizer proxy (no effect until proxy ships)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New("not logged in — run 'relaya login' first")
	}
	if err != nil {
		return err
	}

	rest := fs.Args()
	var toolName string
	switch len(rest) {
	case 0:
		toolName, err = interactivePicker()
		if err != nil {
			return err
		}
	case 1:
		toolName = rest[0]
	default:
		return fmt.Errorf("usage: relaya use <tool>")
	}

	t, err := tools.Lookup(toolName)
	if err != nil {
		return err
	}

	// The device-auth access token can't relay (it's a management
	// credential); resolve the account's relay API key instead.
	relayKey, err := resolveRelayKey(creds)
	if err != nil {
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
	return tools.Exec(t, env)
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
