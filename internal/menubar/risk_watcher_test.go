package menubar

import (
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
)

func TestApplyChannelRiskDelta(t *testing.T) {
	tests := []struct {
		name        string
		initial     map[int]int
		channels    []api.SellerChannel
		wantNotes   int    // number of notifications fired
		wantInTitle string // substring; "" to skip
		wantCache   map[int]int
	}{
		{
			name:    "first observation seeds cache, no notify",
			initial: nil,
			channels: []api.SellerChannel{
				{ID: 1, Name: "claude", Status: channelStatusEnabled},
				{ID: 2, Name: "gemini", Status: channelStatusAutoDisable},
			},
			wantNotes: 0,
			wantCache: map[int]int{1: channelStatusEnabled, 2: channelStatusAutoDisable},
		},
		{
			name:    "enabled → auto-disabled fires notification",
			initial: map[int]int{1: channelStatusEnabled},
			channels: []api.SellerChannel{
				{ID: 1, Name: "claude-prod", Status: channelStatusAutoDisable},
			},
			wantNotes:   1,
			wantInTitle: "channel auto-disabled",
			wantCache:   map[int]int{1: channelStatusAutoDisable},
		},
		{
			name:    "enabled → manual-disable is silent (user did it)",
			initial: map[int]int{1: channelStatusEnabled},
			channels: []api.SellerChannel{
				{ID: 1, Name: "claude-prod", Status: channelStatusManualDisable},
			},
			wantNotes: 0,
			wantCache: map[int]int{1: channelStatusManualDisable},
		},
		{
			name:    "auto-disabled → enabled (re-enable) is silent",
			initial: map[int]int{1: channelStatusAutoDisable},
			channels: []api.SellerChannel{
				{ID: 1, Name: "claude-prod", Status: channelStatusEnabled},
			},
			wantNotes: 0,
			wantCache: map[int]int{1: channelStatusEnabled},
		},
		{
			name: "multi-channel: only the freshly disabled one fires",
			initial: map[int]int{
				1: channelStatusEnabled,
				2: channelStatusEnabled,
				3: channelStatusAutoDisable, // already off
			},
			channels: []api.SellerChannel{
				{ID: 1, Name: "claude", Status: channelStatusAutoDisable}, // fresh
				{ID: 2, Name: "gemini", Status: channelStatusEnabled},     // unchanged
				{ID: 3, Name: "codex", Status: channelStatusAutoDisable},  // unchanged
			},
			wantNotes:   1,
			wantInTitle: "channel auto-disabled",
			wantCache: map[int]int{
				1: channelStatusAutoDisable,
				2: channelStatusEnabled,
				3: channelStatusAutoDisable,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			notes := captureNotifier(t)
			c := newForTest(&fakeMenu{})
			c.channelStatusCache = tc.initial
			c.applyChannelRiskDelta(tc.channels)

			if got := len(*notes); got != tc.wantNotes {
				t.Errorf("notifications = %d, want %d: %+v", got, tc.wantNotes, *notes)
			}
			if tc.wantNotes > 0 && tc.wantInTitle != "" {
				if !contains((*notes)[0].title, tc.wantInTitle) {
					t.Errorf("title %q missing %q", (*notes)[0].title, tc.wantInTitle)
				}
			}
			for id, want := range tc.wantCache {
				if got := c.channelStatusCache[id]; got != want {
					t.Errorf("cache[%d] = %d, want %d", id, got, want)
				}
			}
			if len(c.channelStatusCache) != len(tc.wantCache) {
				t.Errorf("cache size = %d, want %d (cache=%+v)",
					len(c.channelStatusCache), len(tc.wantCache), c.channelStatusCache)
			}
		})
	}
}
