// CLI jump-session client — the anti-phishing handshake from
// EVERYAPI §7-5 Layer 3. The CLI gets back a {url, phrase} pair,
// prints the phrase to the terminal, asks the user to confirm,
// and only then opens the browser. The dashboard page the URL
// points at fetches and displays the same phrase.
package api

import (
	"context"
	"fmt"
)

// JumpSessionResult is the start-side payload. URL is what the CLI
// should open; VerificationPhrase is the emoji string the user
// should see ALSO displayed at the top of that page. ExpiresIn is
// for the CLI to render a "this expires in N minutes" hint.
type JumpSessionResult struct {
	SessionID          string
	URL                string
	VerificationPhrase string
	ExpiresIn          int
}

// CreateJumpSession mints a jump session for the given intent.
// Valid intents on the backend today: "topup", "wallet", "channels".
// Anything else returns a 400 with a server-formatted message that
// surfaces verbatim via the standard *APIError envelope.
func (c *Client) CreateJumpSession(ctx context.Context, intent string) (*JumpSessionResult, error) {
	body := map[string]string{"intent": intent}
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			SessionID          string `json:"session_id"`
			URL                string `json:"url"`
			VerificationPhrase string `json:"verification_phrase"`
			ExpiresIn          int    `json:"expires_in"`
		} `json:"data"`
	}
	if err := c.do(ctx, "POST", "/api/cli/jump-session", body, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("create jump session: %s", env.Message)
	}
	return &JumpSessionResult{
		SessionID:          env.Data.SessionID,
		URL:                env.Data.URL,
		VerificationPhrase: env.Data.VerificationPhrase,
		ExpiresIn:          env.Data.ExpiresIn,
	}, nil
}
