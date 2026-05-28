package edge

import (
	"strings"
	"testing"
)

// TestRunHelp verifies the usage block is non-empty and mentions every
// declared subcommand. The Run dispatcher returns nil on help/--help/-h
// after printing.
func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		if err := Run([]string{arg}); err != nil {
			t.Errorf("Run(%q) returned error: %v", arg, err)
		}
	}
	for _, sub := range []string{"register", "start", "status", "stop", "logs", "models", "update", "rename", "pause", "resume", "remove"} {
		if !strings.Contains(edgeUsage(), sub) {
			t.Errorf("edgeUsage missing subcommand %q", sub)
		}
	}
}

// TestRunUnknownSubcommand asserts the dispatcher rejects garbage with
// a non-nil error, not a silent fall-through.
func TestRunUnknownSubcommand(t *testing.T) {
	if err := Run([]string{"flobnar"}); err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

// TestRunNoArgs expects a usable error pointing at help. The exact
// string can change; check it's a non-nil error and that the usage
// text was emitted (we don't capture stdout here so the existence of
// the error is enough — see TestRunHelp for the usage content check).
func TestRunNoArgs(t *testing.T) {
	if err := Run(nil); err == nil {
		t.Fatal("expected error for empty args")
	}
}
