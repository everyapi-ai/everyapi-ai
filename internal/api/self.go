package api

import (
	"context"
	"fmt"
)

// SelfData is the subset of /api/user/self the CLI reads. The full
// payload has affiliate / settings / etc. fields the CLI doesn't
// need today; keeping the struct narrow avoids accidental coupling.
type SelfData struct {
	ID           int    `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	Quota        int64  `json:"quota"`
	UsedQuota    int64  `json:"used_quota"`
	RequestCount int64  `json:"request_count"`
	// SellerQuota — pending channel-marketplace earnings. The
	// relaya_seller_withdraw MCP tool reads this to decide the
	// default "all" transfer amount. Zero when the user has never
	// participated in the marketplace.
	SellerQuota int `json:"seller_quota"`
}

func (c *Client) GetSelf(ctx context.Context) (*SelfData, error) {
	var env struct {
		Success bool     `json:"success"`
		Message string   `json:"message"`
		Data    SelfData `json:"data"`
	}
	if err := c.do(ctx, "GET", "/api/user/self", nil, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("get self: %s", env.Message)
	}
	return &env.Data, nil
}

// StatusData is the subset of /api/status the CLI reads. We use
// quota_per_unit to convert the integer quota field into a USD figure
// for display. The /api/status endpoint is unauthenticated so this
// works before login too.
type StatusData struct {
	QuotaPerUnit float64 `json:"quota_per_unit"`
}

func (c *Client) GetStatus(ctx context.Context) (*StatusData, error) {
	var env struct {
		Success bool       `json:"success"`
		Message string     `json:"message"`
		Data    StatusData `json:"data"`
	}
	if err := c.do(ctx, "GET", "/api/status", nil, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("get status: %s", env.Message)
	}
	return &env.Data, nil
}

// ProbeRelayToken exercises the relay auth path so the CLI can tell,
// BEFORE handing the token to a tool, whether it will actually relay.
// GET /v1/models runs the same middleware.TokenAuth /
// ValidateUserToken as /v1/messages, so an exhausted / expired /
// disabled token returns the same 401 here. This matters because
// /api/user/self uses UserAuth and skips ValidateUserToken's
// quota/expiry gates — a healthy `relaya status` does NOT imply the
// token can relay. The endpoint is a free, non-billable model list;
// only the auth gate is significant. Sends just the bearer (no
// Relaya-User-Id), mirroring exactly what a relayed tool sends.
// Returns nil on 2xx; *APIError (use IsUnauthorized) otherwise.
func (c *Client) ProbeRelayToken(ctx context.Context) error {
	return c.do(ctx, "GET", "/v1/models", nil, nil)
}
