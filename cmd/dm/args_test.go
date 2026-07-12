package dm

import "testing"

func TestFixedCommandsRejectExtraPositionals(t *testing.T) {
	cases := map[string]func() error{
		"threads": func() error { return runThreads([]string{"extra"}) }, "contacts": func() error { return runContacts([]string{"extra"}) },
		"count": func() error { return runCount([]string{"extra"}) }, "open": func() error { return runOpen([]string{"1", "extra"}) },
		"messages": func() error { return runMessages([]string{"1", "extra"}) }, "read": func() error { return runRead([]string{"1", "extra"}) },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("accepted extra positional")
			}
		})
	}
}
