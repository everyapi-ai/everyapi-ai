package cmd

import (
	"strings"
	"testing"
)

func TestTopupRejectsExtraPositionalsBeforeAPI(t *testing.T) {
	if err := Topup([]string{"extra"}); err == nil || !strings.Contains(err.Error(), "unexpected positional") {
		t.Fatalf("did not reject extra positional explicitly: %v", err)
	}
}
