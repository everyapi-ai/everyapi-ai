package api

import (
	"context"
	"fmt"
)

// TokenStatusEnabled mirrors backend common.TokenStatusEnabled (1).
// Only enabled tokens can relay; the others (disabled/expired/
// exhausted) would 401 at ValidateUserToken, so the CLI must skip
// them when picking a relay key.
const TokenStatusEnabled = 1

// TokenSummary is the subset of a relay API token the CLI needs to
// pick one. /api/token/ returns the key MASKED, so we never read it
// here — the full key comes from TokenKey(id).
type TokenSummary struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status int    `json:"status"`
	// Group is the token's routing group. /api/token/ returns it on
	// every row (controller buildMaskedTokenResponse → model.Token,
	// which carries `json:"group"`). `everyapi use --group` filters on
	// it so a buyer can deliberately route to the channels bound to a
	// given group (e.g. a BytePlus-only group) instead of the newest
	// enabled key.
	Group string `json:"group"`
}

// ListTokens returns the user's relay API tokens (management API,
// UserAuth — caller must have set WithUserID). Only the first page is
// read: a user picking a relay key for the CLI realistically has a
// handful of tokens, and full pagination would be overkill here.
func (c *Client) ListTokens(ctx context.Context) ([]TokenSummary, error) {
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Items []TokenSummary `json:"items"`
		} `json:"data"`
	}
	if err := c.do(ctx, "GET", "/api/token/", nil, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("list tokens: %s", env.Message)
	}
	return env.Data.Items, nil
}

// TokenKey fetches the full plaintext relay key (sk-everyapi-…) for a
// token id. The backend audit-logs this disclosure (it's the same
// endpoint the dashboard's "show key" button hits).
func (c *Client) TokenKey(ctx context.Context, id int) (string, error) {
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := c.do(ctx, "POST", fmt.Sprintf("/api/token/%d/key", id), nil, &env); err != nil {
		return "", err
	}
	if !env.Success || env.Data.Key == "" {
		return "", fmt.Errorf("get token key: %s", env.Message)
	}
	return env.Data.Key, nil
}
