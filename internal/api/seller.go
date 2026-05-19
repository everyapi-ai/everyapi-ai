// Seller-side API client surfaces — list a user's mounted seller
// channels and transfer pending seller earnings into the main
// wallet. Both endpoints are user-authenticated; the access token
// from credentials.json is the bearer.
package api

import (
	"context"
	"fmt"
)

// SellerChannel mirrors the fields the MCP `everyapi_seller_list`
// tool surfaces to the AI agent. The backend `Channel` struct is
// larger — we only decode what we render, so a future backend field
// addition doesn't break this client.
//
// Status meanings (aligned with the server's ChannelStatus enum):
//
//	1 = enabled
//	2 = manually disabled by the seller
//	3 = auto-disabled by the health-check worker
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

// SellerEligibility mirrors backend controller.SellerEligibilityResponse —
// the subset the CLI surfaces in `everyapi seller setup` to tell the user
// up-front which mount gate they're failing (account age, email
// verification, consume log, channel cap). The backend re-checks every
// gate at submit, so this is a soft hint, not a security boundary.
type SellerEligibility struct {
	Eligible           bool `json:"eligible"`
	MarketplaceEnabled bool `json:"marketplace_enabled"`
	AccountActive      bool `json:"account_active"`
	EmailVerified      bool `json:"email_verified"`
	AccountAgeOK       bool `json:"account_age_ok"`
	MinAgeDays         int  `json:"min_age_days"`
	HasConsumeLog      bool `json:"has_consume_log"`
	ChannelCount       int  `json:"channel_count"`
	ChannelCap         int  `json:"channel_cap"`
	UnderCap           bool `json:"under_cap"`
}

// GetSellerEligibility hits GET /api/seller/eligibility. Used by the
// `seller setup` wizard to render a checklist BEFORE the user types a
// key — failing gates server-side after they've typed credentials would
// be a frustrating UX.
func (c *Client) GetSellerEligibility(ctx context.Context) (*SellerEligibility, error) {
	var env struct {
		Success bool              `json:"success"`
		Message string            `json:"message"`
		Data    SellerEligibility `json:"data"`
	}
	if err := c.do(ctx, "GET", "/api/seller/eligibility", nil, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("get seller eligibility: %s", env.Message)
	}
	return &env.Data, nil
}

// SellerChannelCreate matches backend controller.SellerChannelCreate.
// Whitelisted fields only — the backend strips anything else (group,
// owner_user_id, priority/weight, base_url) per PRODUCT §4.1 / §4.4a.
// Name / Type / Keys / Models are the practical minimum.
//
// Keys is the multi-key backup pool (B2, PRODUCT §4.5): one channel,
// N equivalent credentials, per-key failover. KeyRemarks is
// index-aligned with Keys; the backend rejects multi-key sets that
// contain an OAuth/JSON-blob credential (those must be their own
// single-key channel). A single-element Keys is the ordinary
// single-key channel; the backend's normaliseLegacyKey shim accepts
// the older single `key` field too, but new code should send Keys.
type SellerChannelCreate struct {
	Name           string   `json:"name"`
	Type           int      `json:"type"`
	Keys           []string `json:"keys"`
	KeyRemarks     []string `json:"key_remarks,omitempty"`
	Models         string   `json:"models"`
	ModelMapping   string   `json:"model_mapping,omitempty"`
	StatusCodeMap  string   `json:"status_code_mapping,omitempty"`
	TestModel      string   `json:"test_model,omitempty"`
	Other          string   `json:"other,omitempty"`
	OtherSettings  string   `json:"settings,omitempty"`
	ParamOverride  string   `json:"param_override,omitempty"`
	HeaderOverride string   `json:"header_override,omitempty"`
	Remark         string   `json:"remark,omitempty"`
}

// CreateSellerChannel POSTs to /api/seller/channel and returns the new
// channel's id on success. Surfaces the backend's specific error
// messages (eligibility 403, type-not-allowed 422, cap-reached 403)
// via the standard *APIError envelope so the caller can render them
// verbatim — they're already user-facing.
func (c *Client) CreateSellerChannel(ctx context.Context, req SellerChannelCreate) (int, error) {
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := c.do(ctx, "POST", "/api/seller/channel", req, &env); err != nil {
		return 0, err
	}
	if !env.Success {
		return 0, fmt.Errorf("create seller channel: %s", env.Message)
	}
	return env.Data.ID, nil
}
