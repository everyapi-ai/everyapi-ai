package edge

import (
	"strings"
	"testing"
)

func TestFlagOnlyCommandsRejectPositionalsBeforeSideEffects(t *testing.T) {
	cases := [][]string{
		{"register", "extra"},
		{"list", "extra"},
		{"start", "extra"},
		{"status", "extra"},
		{"stop", "extra"},
		{"logs", "extra"},
		{"update", "extra"},
		{"rename", "--name", "new", "extra"},
		{"pause", "extra"},
		{"resume", "extra"},
		{"remove", "--yes", "extra"},
	}
	for _, args := range cases {
		t.Run(args[0], func(t *testing.T) {
			err := Run(args)
			if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
				t.Fatalf("Run(%v) error = %v, want positional-argument rejection", args, err)
			}
		})
	}
}
