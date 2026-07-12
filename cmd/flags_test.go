package cmd

import (
	"strings"
	"testing"
)

func TestCoreFlagOnlyCommandsRejectPositionalsBeforeSideEffects(t *testing.T) {
	cases := map[string]func() error{
		"login":   func() error { return Login([]string{"extra"}) },
		"status":  func() error { return Status([]string{"extra"}) },
		"update":  func() error { _, err := updateRun([]string{"extra"}); return err },
		"version": func() error { return Version([]string{"extra"}) },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
				t.Fatalf("error = %v, want positional-argument rejection", err)
			}
		})
	}
}
