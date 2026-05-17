// Seller-side API client surfaces — list a user's mounted seller
// channels and transfer pending seller earnings into the main
// wallet. Both endpoints are user-authenticated; the access token
// from credentials.json is the bearer.
package api

import (
	"context"
	"fmt"
)

// SellerChannel mirrors the fields the MCP `relaya_seller_list`
// tool surfaces to the AI agent. The backend `Channel` struct is
// larger — we only decode what we render, so a future backend field
// addition doesn't break this client.
//
// Status meanings (aligned with backend/internal/common.ChannelStatus*):
//
//	1 = enabled
//	2 = manually disabled by the seller
//	3 = auto-disabled by the health-check worker (marketplace §4.5)
type SellerChannel struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Type   int    `json:"type"`
	Status int    `json:"status"`
	Models string `json:"models"`
	// Group / TestModel / etc. are omitted intentionally — adding
	// them later is non-breaking because the field is `omitempty`
	// on the backend, so a missing key in the response doesn't
	// trigger a decode error.
}

// ListSellerChannels hits GET /api/seller/channel and returns the
// caller's mounted channels. Pagination is hardcoded to page 1
// with the backend's default 50-per-page limit — V0 sellers cap at
// 10 channels (marketplace.max_channels_per_seller), so one page is
// the entire set. Bump if/when the cap is raised.
func (c *Client) ListSellerChannels(ctx context.Context) ([]SellerChannel, error) {
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Items    []SellerChannel `json:"items"`
			Total    int             `json:"total"`
			Page     int             `json:"page"`
			PageSize int             `json:"page_size"`
		} `json:"data"`
	}
	if err := c.do(ctx, "GET", "/api/seller/channel", nil, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("list seller channels: %s", env.Message)
	}
	return env.Data.Items, nil
}

// TransferSellerQuota moves `quota` units from SellerQuota into the
// caller's main Quota. Wraps POST /api/user/seller_transfer.
// Caller is responsible for choosing the amount (the MCP tool's
// "all" semantics are implemented one layer up by querying GetSelf
// first).
//
// Returns nil on success. A 4xx with a backend-formatted message
// (e.g. "frozen", "insufficient seller balance") surfaces via the
// standard *APIError shape.
func (c *Client) TransferSellerQuota(ctx context.Context, quota int) error {
	if quota <= 0 {
		return fmt.Errorf("quota must be positive, got %d", quota)
	}
	body := map[string]int{"quota": quota}
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := c.do(ctx, "POST", "/api/user/seller_transfer", body, &env); err != nil {
		return err
	}
	if !env.Success {
		return fmt.Errorf("transfer seller quota: %s", env.Message)
	}
	return nil
}
