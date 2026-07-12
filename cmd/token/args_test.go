package token

import (
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/api"
)

func TestCommandsRejectExtraPositionalsBeforeAPI(t *testing.T) {
	cases := map[string]func() error{
		"list":    func() error { return runList([]string{"extra"}) },
		"show":    func() error { return runShow([]string{"1", "extra"}) },
		"key":     func() error { return runKey([]string{"1", "extra"}) },
		"usage":   func() error { return runUsage([]string{"sk-test", "extra"}) },
		"create":  func() error { return runCreate([]string{"--name", "n", "--unlimited", "extra"}) },
		"update":  func() error { return runUpdate([]string{"1", "--name", "n", "extra"}) },
		"enable":  func() error { return runSetStatus([]string{"1", "extra"}, api.TokenStatusEnabled) },
		"disable": func() error { return runSetStatus([]string{"1", "extra"}, api.TokenStatusDisabled) },
		"revoke":  func() error { return runRevoke([]string{"1", "extra", "-y"}) },
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
