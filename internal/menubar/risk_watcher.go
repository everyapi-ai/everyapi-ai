package menubar

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
)

// Channel status enum values from internal/api/seller.go — copied as
// constants here so the controller code reads as intent rather than
// magic numbers. Keep in sync with the backend's ChannelStatus.
const (
	channelStatusEnabled       = 1
	channelStatusManualDisable = 2
	channelStatusAutoDisable   = 3
)

// riskPollInterval is the cadence at which the watcher checks
// `/api/seller/channel` for status transitions. Five minutes
// balances responsiveness with rate-limit politeness: the backend's
// auto-disable trigger (7-day 3-strike health worker, §4.6) doesn't
// fire faster than once per minute, and a seller realistically
// reads a notification minutes after delivery anyway.
const riskPollInterval = 5 * time.Minute

// startRiskWatcher launches the background risk-polling loop.
// Intended to be called once per Controller from Run(). It runs
// until ctx is cancelled; the loop no-ops on each tick when the
// controller is not signed in, so a sign-out doesn't need to tear
// down or restart anything.
func (c *Controller) startRiskWatcher(ctx context.Context) {
	tk := time.NewTicker(riskPollInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			c.pollChannelRisk(ctx)
		}
	}
}

// pollChannelRisk fetches the channel list once and notifies on any
// channel that transitioned `enabled → auto-disabled` since the last
// poll. First observations seed the cache without notifying — we
// don't know whether a currently-disabled channel just flipped or
// has been off for a week.
//
// Manual disables (status=2, seller toggled it themselves) are
// deliberately silent: the user did that, they don't need a toast.
// Re-enables silently update the cache so the next disable fires
// fresh.
func (c *Controller) pollChannelRisk(ctx context.Context) {
	c.mu.Lock()
	creds := c.creds
	c.mu.Unlock()
	if creds == nil {
		return
	}

	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	channels, err := client.ListSellerChannels(pollCtx)
	if err != nil {
		if api.IsUnauthorized(err) {
			// Session expired between data refresh and risk poll —
			// let the data refresh path handle the cleanup so we
			// don't race it.
			return
		}
		log.Printf("menubar: list seller channels: %v", err)
		return
	}
	c.applyChannelRiskDelta(channels)
}

// applyChannelRiskDelta is split out so tests can drive the diff
// logic directly without spinning a fake server.
func (c *Controller) applyChannelRiskDelta(channels []api.SellerChannel) {
	c.mu.Lock()
	if c.channelStatusCache == nil {
		c.channelStatusCache = make(map[int]int)
	}
	type toFire struct{ name string }
	var fires []toFire
	for _, ch := range channels {
		prev, seen := c.channelStatusCache[ch.ID]
		c.channelStatusCache[ch.ID] = ch.Status
		if !seen {
			continue
		}
		if prev == channelStatusEnabled && ch.Status == channelStatusAutoDisable {
			fires = append(fires, toFire{name: ch.Name})
		}
	}
	// Cap at channelSubmenuSlots — the dashboard handles overflow.
	shown := len(channels)
	if shown > channelSubmenuSlots {
		shown = channelSubmenuSlots
	}
	c.lastChannels = append(c.lastChannels[:0], channels[:shown]...)
	rows := make([]channelMenuRow, shown)
	for i := 0; i < shown; i++ {
		rows[i] = channelMenuRow{
			ID:     channels[i].ID,
			Title:  channelMenuTitle(channels[i]),
			Status: channels[i].Status,
		}
	}
	c.mu.Unlock()

	muted := c.prefs.MuteRisk
	c.menu.applyChannels(rows)
	c.recomputeIconState()
	if muted {
		return
	}
	for _, f := range fires {
		notify(
			"EveryAPI — channel auto-disabled",
			fmt.Sprintf("\"%s\" was auto-disabled by the health-check worker. Open the dashboard to inspect.", f.name),
		)
	}
}

// channelMenuTitle renders a channel as a single line for the
// submenu: name plus a status marker. Compact so the macOS menu
// width doesn't blow up on long channel names.
func channelMenuTitle(ch api.SellerChannel) string {
	switch ch.Status {
	case channelStatusEnabled:
		return ch.Name + "  ✓"
	case channelStatusManualDisable:
		return ch.Name + "  ⏸"
	case channelStatusAutoDisable:
		return ch.Name + "  ⚠"
	default:
		return ch.Name
	}
}
