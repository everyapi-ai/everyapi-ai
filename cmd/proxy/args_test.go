package proxy

import (
	"strings"
	"testing"
)

func TestFlagOnlyCommandsRejectPositionalsBeforeSideEffects(t *testing.T) {
	for name, run := range map[string]func([]string) error{
		"start":     proxyStart,
		"stop":      proxyStop,
		"configure": proxyConfigure,
		"status":    proxyStatus,
	} {
		t.Run(name, func(t *testing.T) {
			err := run([]string{"extra"})
			if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
				t.Fatalf("%s error = %v, want positional-argument rejection", name, err)
			}
		})
	}
}
