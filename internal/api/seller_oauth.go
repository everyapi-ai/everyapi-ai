// Seller-side OAuth onboarding (CLI side of /api/seller/codex/device/*).
// The flow is RFC 8628-shaped, adapted for OpenAI's custom device
// endpoints — see backend service/codex_device_oauth.go for the wire
// quirks. CLI's job is the polling loop + browser launch; the auth
// state (device_auth_id, user_code, channel name + models) lives in
// a backend session keyed by a cookie, which is why the client MUST
// be built with WithCookieJar() before calling these.
package api

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SellerCodexOAuthStart is the start-flow payload — what the seller
// would have to type into `seller add-key` for the same effect, plus
// the device-auth artifacts.
type SellerCodexOAuthStart struct {
	FlowID                  string `json:"flow_id"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

// StartSellerCodexOAuth kicks off the device-auth flow. name + models
// are required server-side (the channel can't be mounted without
// them) — the wizard / scripted caller must collect them up front.
// Caller MUST have invoked WithCookieJar on the client; without it
// the matching poll won't see the session-stashed device_auth_id.
func (c *Client) StartSellerCodexOAuth(ctx context.Context, name, models string) (*SellerCodexOAuthStart, error) {
	body := map[string]string{"name": name, "models": models}
	var env struct {
		Success bool                  `json:"success"`
		Message string                `json:"message"`
		Data    SellerCodexOAuthStart `json:"data"`
	}
	if err := c.do(ctx, "POST", "/api/seller/codex/device/start", body, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("start codex device-auth: %s", env.Message)
	}
	return &env.Data, nil
}

// SellerCodexPollState differentiates "keep polling" from terminal
// states. Mirror of the backend's code strings; the CLI poll loop
// switches on this.
type SellerCodexPollState int

const (
	SellerCodexPollPending SellerCodexPollState = iota
	SellerCodexPollSlowDown
	SellerCodexPollExpired
	SellerCodexPollDenied
	SellerCodexPollAuthorized
)

// SellerCodexPollResult carries the authorized-state payload alongside
// the protocol state. ChannelID is the seller's freshly minted channel
// row (only populated when State == Authorized).
type SellerCodexPollResult struct {
	State     SellerCodexPollState
	ChannelID int
	Email     string
	AccountID string
}

// ErrSellerCodexPollExpired / ErrSellerCodexPollDenied are sentinels
// the cmd layer renders into user-friendly messages. Mirror device-auth
// login's ErrDeviceAuthExpired / ErrDeviceAuthDenied pattern.
var (
	ErrSellerCodexPollExpired = errors.New("device authorization expired")
	ErrSellerCodexPollDenied  = errors.New("device authorization denied")
)

// sellerCodexPollWire matches the success-path `data` payload from
// PollSellerCodexDeviceOAuth: nested channel struct + flat email /
// account_id. Non-success states carry their state in the outer
// envelope's `code` field (see env struct below).
type sellerCodexPollWire struct {
	Channel struct {
		ID int `json:"id"`
	} `json:"channel"`
	Email     string `json:"email"`
	AccountID string `json:"account_id"`
}

// SellerCodexPoll makes one poll request. Use PollSellerCodexUntilDone
// for the full loop — this is exported for testability.
func (c *Client) SellerCodexPoll(ctx context.Context, flowID string) (*SellerCodexPollResult, error) {
	body := map[string]string{"flow_id": flowID}
	var env struct {
		Success bool                `json:"success"`
		Message string              `json:"message"`
		Code    string              `json:"code"`
		Data    sellerCodexPollWire `json:"data"`
	}
	// The poll endpoint returns HTTP 200 for non-terminal states (the
	// `code` field carries pending/slow_down/expired/denied) and HTTP
	// 200 for success too; only transport / server errors come back
	// as a non-2xx and get translated to *APIError by do().
	if err := c.do(ctx, "POST", "/api/seller/codex/device/poll", body, &env); err != nil {
		return nil, err
	}
	out := &SellerCodexPollResult{}
	if env.Success {
		out.State = SellerCodexPollAuthorized
		out.ChannelID = env.Data.Channel.ID
		out.Email = env.Data.Email
		out.AccountID = env.Data.AccountID
		return out, nil
	}
	// Non-success envelope: classify by `code`. An unknown code is a
	// server-side bug — surface as error rather than silently looping.
	switch env.Code {
	case "pending":
		out.State = SellerCodexPollPending
	case "slow_down":
		out.State = SellerCodexPollSlowDown
	case "expired":
		out.State = SellerCodexPollExpired
	case "denied":
		out.State = SellerCodexPollDenied
	default:
		return nil, fmt.Errorf("seller codex poll: %s", env.Message)
	}
	return out, nil
}

// PollSellerCodexUntilDone runs the poll loop until success / terminal
// failure / ctx cancel. Mirrors PollUntilDone (device-auth login) —
// see that one's commentary on the transient-retry budget; we share
// the same constant.
func (c *Client) PollSellerCodexUntilDone(ctx context.Context, flowID string, initialIntervalSecs int) (*SellerCodexPollResult, error) {
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
		res, err := c.SellerCodexPoll(ctx, flowID)
		if err != nil {
			if errors.As(err, new(*APIError)) {
				return nil, err
			}
			transientFails++
			if transientFails > pollTransientRetryBudget {
				return nil, err
			}
			continue
		}
		transientFails = 0
		switch res.State {
		case SellerCodexPollAuthorized:
			return res, nil
		case SellerCodexPollExpired:
			return nil, ErrSellerCodexPollExpired
		case SellerCodexPollDenied:
			return nil, ErrSellerCodexPollDenied
		case SellerCodexPollSlowDown:
			interval += 5 * time.Second
		case SellerCodexPollPending:
			// keep current interval
		}
	}
}
