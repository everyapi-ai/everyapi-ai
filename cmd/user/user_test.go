package user

import "testing"

func TestOAuthHelp(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		if err := runOAuth([]string{arg}); err != nil {
			t.Fatalf("account oauth %s returned error: %v", arg, err)
		}
	}
}
