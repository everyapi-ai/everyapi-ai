package admin

import (
	"strings"
	"testing"
)

func TestReadCommandsRejectExtraPositionalsBeforeAPI(t *testing.T) {
	cases := map[string]func() error{
		"user list":         func() error { return adminUserList([]string{"extra"}) },
		"user search":       func() error { return adminUserSearch([]string{"q", "extra"}) },
		"user show":         func() error { return adminUserShow([]string{"1", "extra"}) },
		"channel test":      func() error { return adminChannelTest([]string{"1", "extra"}) },
		"log tail":          func() error { return adminLogTail([]string{"extra"}) },
		"abuse list":        func() error { return adminAbuseList([]string{"extra"}) },
		"abuse show":        func() error { return adminAbuseShow([]string{"1", "extra"}) },
		"audit":             func() error { return adminAudit([]string{"extra"}) },
		"redemption search": func() error { return adminRedemptionSearch([]string{"q", "extra"}) },
		"redemption show":   func() error { return adminRedemptionShow([]string{"1", "extra"}) },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || (!strings.Contains(err.Error(), "positional") && !strings.Contains(err.Error(), "expected")) {
				t.Fatalf("error = %v, want positional-argument rejection", err)
			}
		})
	}
}
