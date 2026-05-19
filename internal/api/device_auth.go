// Device authorization grant — RFC 8628 shape adapted to the EveryAPI
// API envelope (`{success, message, data}`). The CLI starts a flow,
// renders the user_code + verification_uri to the terminal, then
// polls until the user confirms in their browser.
package api

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DeviceAuthStart kicks off a new device-auth flow. The returned
// payload has the user-facing user_code and the URL the user must
// visit; the device_code is the CLI's polling identity (not shown to
// the user).
type DeviceAuthStartResp struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

func (c *Client) DeviceAuthStart(ctx context.Context) (*DeviceAuthStartResp, error) {
	var env struct {
		Success bool                `json:"success"`
		Message string              `json:"message"`
		Data    DeviceAuthStartResp `json:"data"`
	}
	if err := c.do(ctx, "POST", "/api/cli/device-auth-start", nil, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("device-auth-start: %s", env.Message)
	}
	return &env.Data, nil
}

// DeviceAuthPollState differentiates "keep polling" from terminal
// states. The success case carries the access token; the slowdown
// case asks the CLI to lengthen its polling interval.
type DeviceAuthPollState int

const (
	PollPending DeviceAuthPollState = iota
	PollSlowDown
	PollExpired
	PollDenied
	PollAuthorized
)

type DeviceAuthPollResult struct {
	State       DeviceAuthPollState
	AccessToken string
	UserID      int
	Username    string
}

// ErrDeviceAuthExpired is the sentinel returned by PollUntilDone when
// the device code TTL elapses before the user confirms. cmd/login
// renders it as a friendly "took too long, try again".
var ErrDeviceAuthExpired = errors.New("device authorization expired")

// ErrDeviceAuthDenied is returned when the user explicitly denies on
// the web confirmation page.
var ErrDeviceAuthDenied = errors.New("device authorization denied")

// pollTransientRetryBudget caps how many consecutive transport-level
// errors PollUntilDone will absorb before giving up. Three matches
// the worst-case browser flakiness (DNS retry + reconnect + reissue)
// without masking a permanent backend outage indefinitely.
const pollTransientRetryBudget = 3

type deviceAuthPollWire struct {
	Status      string `json:"status"`
	AccessToken string `json:"access_token,omitempty"`
	UserID      int    `json:"user_id,omitempty"`
	Username    string `json:"username,omitempty"`
}

// DeviceAuthPoll makes one poll request. Use PollUntilDone for the
// full loop — this is exported for testability.
func (c *Client) DeviceAuthPoll(ctx context.Context, deviceCode string) (*DeviceAuthPollResult, error) {
	var env struct {
		Success bool               `json:"success"`
		Message string             `json:"message"`
		Data    deviceAuthPollWire `json:"data"`
	}
	body := map[string]string{"device_code": deviceCode}
	if err := c.do(ctx, "POST", "/api/cli/device-auth-poll", body, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("device-auth-poll: %s", env.Message)
	}
	out := &DeviceAuthPollResult{
		AccessToken: env.Data.AccessToken,
		UserID:      env.Data.UserID,
		Username:    env.Data.Username,
	}
	switch env.Data.Status {
	case "pending":
		out.State = PollPending
	case "slow_down":
		out.State = PollSlowDown
	case "expired":
		out.State = PollExpired
	case "denied":
		out.State = PollDenied
	case "authorized":
		out.State = PollAuthorized
	default:
		return nil, fmt.Errorf("device-auth-poll: unknown status %q", env.Data.Status)
	}
	return out, nil
}

// PollUntilDone runs the poll loop with adaptive backoff until the
// flow terminates (success, expiry, deny) or the context is canceled.
// The poller may briefly outlast `expiresIn` if the server says
// "slow_down" — that's fine, expiry returns the same sentinel.
//
// Transient HTTP errors (network blip, DNS hiccup, captive-portal
// drop) are retried in-place rather than aborted: the user is at the
// browser approving the code, a 2-second loss of connectivity should
// not kill `everyapi login`. We retry up to `pollTransientRetryBudget`
// times before propagating; a definitive server response (APIError)
// is returned immediately so a real "bad request" doesn't get masked
// by retries.
func (c *Client) PollUntilDone(ctx context.Context, deviceCode string, initialIntervalSecs int) (*DeviceAuthPollResult, error) {
	interval := time.Duration(initialIntervalSecs) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	transientFails := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		res, err := c.DeviceAuthPoll(ctx, deviceCode)
		if err != nil {
			// A server-side error is definitive (the server told us
			// something — bad request, gone, etc.); surface it.
			if errors.As(err, new(*APIError)) {
				return nil, err
			}
			// Network-level error — try again unless we've burnt the
			// budget. The context cancellation case is handled above
			// at the top of the loop, so this only ever retries
			// transient transport failures.
			transientFails++
			if transientFails > pollTransientRetryBudget {
				return nil, err
			}
			continue
		}
		// Reset the transient counter on any successful poll —
		// "pending" answers count as healthy.
		transientFails = 0
		switch res.State {
		case PollAuthorized:
			return res, nil
		case PollExpired:
			return nil, ErrDeviceAuthExpired
		case PollDenied:
			return nil, ErrDeviceAuthDenied
		case PollSlowDown:
			interval += 5 * time.Second
		case PollPending:
			// keep current interval
		}
	}
}
