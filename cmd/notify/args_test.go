package notify

import "testing"

func TestCommandsRejectExtraPositionals(t *testing.T) {
	cases := map[string]func() error{
		"list": func() error { return runList([]string{"extra"}) }, "count": func() error { return runCount([]string{"extra"}) },
		"read": func() error { return runRead([]string{"1", "extra"}) }, "readall": func() error { return runReadAll([]string{"extra"}) },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("accepted extra positional")
			}
		})
	}
}
