package menubar

import (
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
)

// TestRenderIcon emits PNG bytes for every variant and asserts they
// decode + differ from each other. No pixel-perfect comparison
// because the artwork is intentionally placeholder.
func TestRenderIcon(t *testing.T) {
	out := map[IconState][]byte{
		IconStateLoggedOut: renderIcon(IconStateLoggedOut),
		IconStateLoggedIn:  renderIcon(IconStateLoggedIn),
		IconStateAlert:     renderIcon(IconStateAlert),
	}
	for state, png := range out {
		if len(png) == 0 {
			t.Errorf("renderIcon(%v) empty", state)
		}
		if string(png[:4]) != "\x89PNG" {
			t.Errorf("renderIcon(%v) missing PNG signature", state)
		}
	}
	// Each variant must be a distinct byte sequence — equal bytes
	// would mean the state machine flips with no visible effect.
	if string(out[IconStateLoggedOut]) == string(out[IconStateLoggedIn]) {
		t.Error("logged-out and logged-in render identically")
	}
	if string(out[IconStateLoggedIn]) == string(out[IconStateAlert]) {
		t.Error("logged-in and alert render identically")
	}
}

// TestRecomputeIconState walks every (auth-state, channel-list)
// permutation and checks the resulting icon pick.
func TestRecomputeIconState(t *testing.T) {
	tests := []struct {
		name     string
		auth     State
		channels []api.SellerChannel
		want     IconState
	}{
		{"logged-out", StateLoggedOut, nil, IconStateLoggedOut},
		{"logged-out ignores channels", StateLoggedOut,
			[]api.SellerChannel{{Status: channelStatusAutoDisable}}, IconStateLoggedOut},
		{"logged-in no channels", StateLoggedIn, nil, IconStateLoggedIn},
		{"logged-in all healthy", StateLoggedIn,
			[]api.SellerChannel{{Status: channelStatusEnabled}, {Status: channelStatusEnabled}}, IconStateLoggedIn},
		{"logged-in manual disable is not alert", StateLoggedIn,
			[]api.SellerChannel{{Status: channelStatusManualDisable}}, IconStateLoggedIn},
		{"logged-in one auto-disabled fires alert", StateLoggedIn,
			[]api.SellerChannel{{Status: channelStatusEnabled}, {Status: channelStatusAutoDisable}}, IconStateAlert},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fm := &fakeMenu{}
			c := newForTest(fm)
			c.state = tc.auth
			c.lastChannels = tc.channels
			c.recomputeIconState()

			last, ok := fm.lastOfKind("icon")
			if !ok {
				t.Fatal("no applyIconState call")
			}
			if last.args[0].(IconState) != tc.want {
				t.Errorf("icon = %v, want %v", last.args[0], tc.want)
			}
		})
	}
}
