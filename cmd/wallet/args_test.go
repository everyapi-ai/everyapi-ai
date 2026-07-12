package wallet

import "testing"

func TestRedeemRejectsExtraPositionalsBeforeAPI(t *testing.T) {
	if err := runRedeem([]string{"code", "extra"}); err == nil {
		t.Fatal("redeem accepted extra positional")
	}
}

func TestFlagOnlyCommandsRejectPositionalsBeforeAPI(t *testing.T) {
	for name, run := range map[string]func([]string) error{
		"history": runHistory,
		"info":    runInfo,
	} {
		t.Run(name, func(t *testing.T) {
			if err := run([]string{"extra"}); err == nil {
				t.Fatal("accepted extra positional")
			}
		})
	}
}
