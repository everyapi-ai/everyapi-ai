// Seller-side Claude OAuth (CLI side of /api/seller/claude/oauth/*).
// Two-step flow — start hands back an authorize_url for the user to
// open; complete takes whatever string the user pasted from
// Anthropic's callback page and finishes the exchange server-side.
//
// Unlike the codex device flow, the user has to copy-paste a string
// out of the browser. Anthropic's OAuth provider fixes its
// redirect_uri to its own console URL — there's no loopback path
// for the CLI to autoreceive the code. The trade-off lives on the
// CLI side: cmd/seller_oauth.go drives the prompt; this client just
// shuttles the HTTP.
//
// Both calls require WithCookieJar — the backend stashes flow state
// (state/verifier/name/models) in a session keyed by cookie, same
// shape as the codex side.
package api

import (
	"context"
	"fmt"
)

// StartSellerClaudeOAuth kicks off an OAuth authorization flow. The
// returned authorize_url is what the user opens in their browser;
// the flow state lives in the session cookie the response sets.
func (c *Client) StartSellerClaudeOAuth(ctx context.Context, name, models string) (authorizeURL string, _ error) {
	body := map[string]string{"name": name, "models": models}
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			AuthorizeURL string `json:"authorize_url"`
		} `json:"data"`
	}
	if err := c.do(ctx, "POST", "/api/seller/claude/oauth/start", body, &env); err != nil {
		return "", err
	}
	if !env.Success {
		return "", fmt.Errorf("start claude oauth: %s", env.Message)
	}
	return env.Data.AuthorizeURL, nil
}

// SellerClaudeOAuthResult carries the post-exchange payload — the new
// channel id + token expiry — that the CLI surfaces to the user.
type SellerClaudeOAuthResult struct {
	ChannelID   int
	ExpiresAt   string
	LastRefresh string
}

// CompleteSellerClaudeOAuth submits whatever the user pasted from
// Anthropic's callback page. Accepts "code#state", the full URL the
// user copied, or just the code; the backend parses all three
// shapes. Returns the channel id on success — the user has nothing
// else to do.
func (c *Client) CompleteSellerClaudeOAuth(ctx context.Context, input string) (*SellerClaudeOAuthResult, error) {
	body := map[string]string{"input": input}
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Channel struct {
				ID int `json:"id"`
			} `json:"channel"`
			ExpiresAt   string `json:"expires_at"`
			LastRefresh string `json:"last_refresh"`
		} `json:"data"`
	}
	if err := c.do(ctx, "POST", "/api/seller/claude/oauth/complete", body, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("complete claude oauth: %s", env.Message)
	}
	return &SellerClaudeOAuthResult{
		ChannelID:   env.Data.Channel.ID,
		ExpiresAt:   env.Data.ExpiresAt,
		LastRefresh: env.Data.LastRefresh,
	}, nil
}
