package admin

import (
	"strings"
	"testing"
)

func TestWriteCommandsRejectExtraPositionalsBeforeAPI(t *testing.T) {
	cases := map[string]func() error{
		"user manage":  func() error { return adminUserManage([]string{"1", "--action", "disable", "extra"}) },
		"user delete":  func() error { return adminUserDelete([]string{"1", "-y", "extra"}) },
		"channel tag":  func() error { return adminChannelTag([]string{"tag", "--disable", "-y", "extra"}) },
		"abuse update": func() error { return adminAbuseUpdate([]string{"1", "--status", "closed", "extra"}) },
		"redemption create": func() error {
			return adminRedemptionCreate([]string{"--name", "n", "--quota", "1", "extra"})
		},
		"redemption update": func() error { return adminRedemptionUpdate([]string{"1", "--name", "n", "extra"}) },
		"redemption status": func() error { return adminRedemptionStatus([]string{"1", "disable", "extra"}) },
		"redemption delete": func() error { return adminRedemptionDelete([]string{"1", "-y", "extra"}) },
		"redemption clear":  func() error { return adminRedemptionClearInvalid([]string{"-y", "extra"}) },
		"channel oauth": func() error {
			return adminChannelAddOAuthAntigravity([]string{"--name", "n", "--models", "m", "extra"})
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || (!strings.Contains(err.Error(), "unexpected positional arguments") && name != "redemption status") {
				t.Fatalf("error = %v, want positional-argument rejection", err)
			}
		})
	}
}
