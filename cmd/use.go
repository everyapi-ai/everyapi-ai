package cmd

import (
	"errors"
	"flag"
	"fmt"
	"strings"

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

	env := t.Env(creds.APIBase, creds.AccessToken)
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
