package checkin

import (
	"strings"
	"testing"
)

func TestClaimHelpDoesNotAuthenticateOrClaim(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Run([]string{"claim", "--help"}); err != nil {
		t.Fatalf("checkin claim --help returned error: %v", err)
	}
}

// A bare `checkin makeup` must fail on the missing date rather than reach the network — and above all must not fall through to the claim path, which would silently burn today's real check-in.
func TestMakeupWithoutADateFailsBeforeAuthenticating(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	err := Run([]string{"makeup"})
	if err == nil {
		t.Fatal("checkin makeup with no date should error")
	}
	if !strings.Contains(err.Error(), "date") {
		t.Fatalf("expected a missing-date error, got %v", err)
	}
}

// The date is accepted as a bare positional as well as via --date; neither form may be swallowed by the positional rejector.
func TestMakeupAcceptsTheDateAsAPositional(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, args := range [][]string{
		{"makeup", "2026-08-01"},
		{"makeup", "--date", "2026-08-01"},
	} {
		err := Run(args)
		// No credentials in this temp home, so the call stops at the auth gate — which proves the date parsed and the command reached the client.
		if err == nil {
			t.Fatalf("%v: expected the not-logged-in error", args)
		}
		if strings.Contains(err.Error(), "date") {
			t.Fatalf("%v: date was not parsed: %v", args, err)
		}
	}
}
