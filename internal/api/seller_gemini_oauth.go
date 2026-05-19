// Seller-side Gemini OAuth (CLI side of /api/seller/gemini/oauth/*).
// Unlike codex device flow + claude paste flow, this is true
// loopback OAuth: the CLI tells the backend what loopback URL it
// listens on, the backend hands back an authorize URL with that
// redirect, Google sends the code straight to the CLI's listener
// when the user finishes consent — no manual paste.
//
// Both calls require WithCookieJar — backend stashes the flow
// state in a session keyed by `everyapi_session` cookie, same as
// the claude/codex paths.
package api

import (
	"context"
	"fmt"
)

// SellerGeminiOAuthStart is the response from /start. AuthorizeURL is
// what the user opens in the browser; State is what the CLI uses to
// correlate the loopback callback (compare against the `state` query
// param Google adds when it redirects).
type SellerGeminiOAuthStart struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
}

// StartSellerGeminiOAuth kicks off a Gemini OAuth flow with the
// CLI-chosen loopback redirect. The backend validates redirectURI
// is a real loopback URL before building the authorize URL — see
// validateLoopbackRedirectURI on the server. State is returned so
// the CLI can verify the callback wasn't from a stale flow.
func (c *Client) StartSellerGeminiOAuth(ctx context.Context, name, models, redirectURI string) (*SellerGeminiOAuthStart, error) {
	body := map[string]string{
		"name":         name,
		"models":       models,
		"redirect_uri": redirectURI,
	}
	var env struct {
		Success bool                   `json:"success"`
		Message string                 `json:"message"`
		Data    SellerGeminiOAuthStart `json:"data"`
	}
	if err := c.do(ctx, "POST", "/api/seller/gemini/oauth/start", body, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("start gemini oauth: %s", env.Message)
	}
	return &env.Data, nil
}

// SellerGeminiOAuthResult is the post-exchange payload — new
// channel id + token expiry — that the CLI prints back to the user.
type SellerGeminiOAuthResult struct {
	ChannelID   int
	ExpiresAt   string
	LastRefresh string
}

// CompleteSellerGeminiOAuth ships the code + state the CLI received
// on its loopback listener. The backend looks up the verifier +
// redirect_uri by state, finishes the token exchange with Google,
// and mints the channel. Returns the new channel id.
func (c *Client) CompleteSellerGeminiOAuth(ctx context.Context, code, state string) (*SellerGeminiOAuthResult, error) {
	body := map[string]string{"code": code, "state": state}
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
	if err := c.do(ctx, "POST", "/api/seller/gemini/oauth/complete", body, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("complete gemini oauth: %s", env.Message)
	}
	return &SellerGeminiOAuthResult{
		ChannelID:   env.Data.Channel.ID,
		ExpiresAt:   env.Data.ExpiresAt,
		LastRefresh: env.Data.LastRefresh,
	}, nil
}
