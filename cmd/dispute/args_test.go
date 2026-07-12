package dispute

import (
	"strings"
	"testing"
)

func TestCommandsRejectExtraPositionalsBeforeAPI(t *testing.T) {
	tests := map[string]func([]string) error{
		"submit": runSubmit,
		"my":     runList,
		"show":   runShow,
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			args := []string{"extra"}
			if name == "show" {
				args = []string{"1", "extra"}
			}
			if err := run(args); err == nil || (!strings.Contains(err.Error(), "unexpected positional") && !strings.Contains(err.Error(), "expected 1 positional")) {
				t.Fatalf("did not reject extra positional explicitly: %v", err)
			}
		})
	}
}

func TestShowHelpBeforeIDValidation(t *testing.T) {
	if err := runShow([]string{"--help"}); err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
}
