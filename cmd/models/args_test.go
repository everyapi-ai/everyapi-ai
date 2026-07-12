package models

import "testing"

func TestCommandsRejectExtraPositionals(t *testing.T) {
	for name, run := range map[string]func([]string) error{"list": runList, "pricing": runPricing, "groups": runGroups} {
		t.Run(name, func(t *testing.T) {
			if err := run([]string{"extra"}); err == nil {
				t.Fatal("accepted extra positional")
			}
		})
	}
}
