package cmd_test

import (
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/cmd/checkin"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/events"
	logcmd "github.com/everyapi-ai/everyapi-ai/v3/cmd/log"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/perf"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/report"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/subscription"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/upstream"
	"github.com/everyapi-ai/everyapi-ai/v3/cmd/usage"
)

func TestFlagOnlySubpackagesRejectPositionalsBeforeSideEffects(t *testing.T) {
	cases := map[string]func() error{
		"checkin status":          func() error { return checkin.Run([]string{"status", "extra"}) },
		"upstream":                func() error { return upstream.Run([]string{"extra"}) },
		"subscription plans":      func() error { return subscription.Run([]string{"plans", "extra"}) },
		"subscription self":       func() error { return subscription.Run([]string{"self", "extra"}) },
		"subscription preference": func() error { return subscription.Run([]string{"preference", "extra"}) },
		"log list":                func() error { return logcmd.Run([]string{"list", "extra"}) },
		"log stat":                func() error { return logcmd.Run([]string{"stat", "extra"}) },
		"log summary":             func() error { return logcmd.Run([]string{"summary", "extra"}) },
		"events":                  func() error { return events.Run([]string{"extra"}) },
		"perf":                    func() error { return perf.Run([]string{"extra"}) },
		"report": func() error {
			return report.Run([]string{"--email", "a@b.c", "--category", "x", "--target-type", "y", "--description", "z", "extra"})
		},
		"usage": func() error { return usage.Run([]string{"extra"}) },
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
