package cmd

import (
	"strings"
	"testing"
)

// TestAuth_NonRoutingPaths covers the dispatcher arms that don't invoke a network-touching subcommand: help/empty print usage and return nil, an unknown subcommand returns an error naming the bad sub. The login/logout/status routing is exercised by their own tests; calling them here would hit credentials / the network.
func TestAuth_NonRoutingPaths(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
		if err := Auth(args); err != nil {
			t.Errorf("Auth(%v) = %v, want nil (usage)", args, err)
		}
	}

	err := Auth([]string{"bogus"})
	if err == nil {
		t.Fatal("Auth(bogus) = nil, want an unknown-subcommand error")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("unknown-sub error should name the bad sub: %q", err.Error())
	}
}
