package demand

import (
	"strings"
	"testing"
)

func TestCommandsRejectExtraPositionalsBeforeAPI(t *testing.T) {
	tests := map[string]func([]string) error{
		"list":   func(args []string) error { return runList(args, false) },
		"my":     func(args []string) error { return runList(args, true) },
		"show":   runShow,
		"submit": runSubmit,
		"cancel": runCancel,
		"remove": runRemove,
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			args := []string{"extra"}
			if name == "show" || name == "cancel" || name == "remove" {
				args = []string{"1", "extra"}
			}
			if err := run(args); err == nil || (!strings.Contains(err.Error(), "unexpected positional") && !strings.Contains(err.Error(), "expected 1 positional")) {
				t.Fatalf("did not reject extra positional explicitly: %v", err)
			}
		})
	}
}

func TestIDCommandHelpBeforeValidation(t *testing.T) {
	for name, run := range map[string]func([]string) error{
		"show":   runShow,
		"cancel": runCancel,
		"remove": runRemove,
	} {
		t.Run(name, func(t *testing.T) {
			if err := run([]string{"--help"}); err != nil {
				t.Fatalf("--help returned error: %v", err)
			}
		})
	}
}
