package mcp

import "testing"

func TestCommandsRejectExtraArgumentsBeforeClientExecution(t *testing.T) {
	tests := map[string]func([]string) error{
		"install":   Install,
		"uninstall": Uninstall,
		"status":    Status,
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			args := []string{"extra"}
			if name != "status" {
				args = []string{"claude", "extra"}
			}
			if err := run(args); err == nil {
				t.Fatal("accepted extra arguments")
			}
		})
	}
}

func TestCommandHelpBeforeExactArgumentValidation(t *testing.T) {
	for name, run := range map[string]func([]string) error{
		"install": Install, "uninstall": Uninstall, "status": Status,
	} {
		t.Run(name, func(t *testing.T) {
			if err := run([]string{"--help"}); err != nil {
				t.Fatalf("--help returned error: %v", err)
			}
		})
	}
}
