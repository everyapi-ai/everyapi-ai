package menubar

import (
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

func TestChannelMenuTitle(t *testing.T) {
	tests := []struct {
		ch   api.SellerChannel
		want string
	}{
		{api.SellerChannel{Name: "claude", Status: channelStatusEnabled}, "claude  ✓"},
		{api.SellerChannel{Name: "g", Status: channelStatusManualDisable}, "g  ⏸"},
		{api.SellerChannel{Name: "r", Status: channelStatusAutoDisable}, "r  ⚠"},
		{api.SellerChannel{Name: "x", Status: 99}, "x"},
	}
	for _, tc := range tests {
		if got := channelMenuTitle(tc.ch); got != tc.want {
			t.Errorf("channelMenuTitle(%+v) = %q, want %q", tc.ch, got, tc.want)
		}
	}
}

func TestChannelStatusLabel(t *testing.T) {
	cases := map[int]string{
		channelStatusEnabled:       "Enabled",
		channelStatusManualDisable: "Manually disabled",
		channelStatusAutoDisable:   "Auto-disabled by health worker — investigate in dashboard",
		99:                         "Unknown status",
	}
	for status, want := range cases {
		if got := channelStatusLabel(status); got != want {
			t.Errorf("channelStatusLabel(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestHandleChannelClick(t *testing.T) {
	browser := stubOpenBrowser(t)
	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: "https://api.example.test", AccessToken: "tok"}
	c.lastChannels = []api.SellerChannel{
		{ID: 7, Name: "claude", Status: channelStatusEnabled},
	}

	c.handleChannelClick(0)
	if len(*browser) != 1 {
		t.Fatalf("browser opens = %v", *browser)
	}
	if !strings.Contains((*browser)[0], "/seller/channels/7") {
		t.Errorf("URL %q missing channel id", (*browser)[0])
	}

	// Out-of-range click — silent no-op.
	c.handleChannelClick(99)
	if len(*browser) != 1 {
		t.Errorf("stale click opened browser: %v", *browser)
	}
}
