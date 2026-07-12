package checkin

import "testing"

func TestClaimHelpDoesNotAuthenticateOrClaim(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Run([]string{"claim", "--help"}); err != nil {
		t.Fatalf("checkin claim --help returned error: %v", err)
	}
}
