package menubar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/oauthloopback"
)

// claudePasteShape pre-validates the `<code>#<state>` string a user
// copies out of Anthropic's callback page. Catches accidental pastes
// (random text on the clipboard) and bounds the input — without
// this, a hostile local app that wrote a crafted payload to the
// clipboard before the user clicked Paste could trip backend error
// paths that echo the input.
var claudePasteShape = regexp.MustCompile(`^[A-Za-z0-9_\-]+#[A-Za-z0-9_\-]+$`)

// claudePasteMaxLen caps the length we send to the backend so an
// adversarial clipboard payload can't be forwarded unbounded. Real
// Anthropic codes are well under 200 chars; 512 gives ample headroom.
const claudePasteMaxLen = 512

// sellerOAuthTimeout caps how long either OAuth flow may sit
// blocked (browser open, user thinking). Matches the CLI's gemini
// default; long enough for a real user, short enough that an
// abandoned flow eventually frees the loopback port and clears the
// Claude paste-pending slot.
const sellerOAuthTimeout = 5 * time.Minute

// promptChannelMeta collects the two free-form inputs both flows
// share. Returns ok=false when the user cancels either modal so
// callers can short-circuit without a fake error.
func promptChannelMeta(provider string) (name, models string, ok bool, err error) {
	name, ok, err = textPrompt(
		"EveryAPI — name for the new "+provider+" channel",
		"Pick a label you'll recognise in the dashboard (e.g. \""+provider+"-personal\").",
		"",
	)
	if !ok || err != nil {
		return "", "", ok, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", false, errors.New("channel name cannot be empty")
	}
	models, ok, err = textPrompt(
		"EveryAPI — models for "+name,
		"Comma-separated model names this channel will serve (e.g. \""+defaultModelsHint(provider)+"\").",
		defaultModelsHint(provider),
	)
	if !ok || err != nil {
		return "", "", ok, err
	}
	models = strings.TrimSpace(models)
	if models == "" {
		return "", "", false, errors.New("models list cannot be empty")
	}
	return name, models, true, nil
}

func defaultModelsHint(provider string) string {
	switch provider {
	case "Claude":
		return "claude-3-5-sonnet,claude-3-opus"
	case "Gemini":
		return "gemini-2.0-flash,gemini-2.0-pro"
	default:
		return ""
	}
}

// startClaudeOAuth runs the half of the Claude flow up to and
// including opening the browser. The HTTP client (with cookie jar)
// is returned so the controller can hold it until the paste-back
// click — backend stores flow state in a session keyed by cookie, so
// /start and /complete MUST share the same client. ok=false signals
// a clean cancellation (user dismissed a modal); err is reserved for
// non-cancellation failures.
func (c *Controller) startClaudeOAuth(ctx context.Context) (client *api.Client, ok bool, err error) {
	c.mu.Lock()
	creds := c.creds
	c.mu.Unlock()
	if creds == nil {
		return nil, false, errors.New("not signed in")
	}

	name, models, ok, err := promptChannelMeta("Claude")
	if err != nil || !ok {
		return nil, ok, err
	}

	cli := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID).WithCookieJar()
	authURL, err := cli.StartSellerClaudeOAuth(ctx, name, models)
	if err != nil {
		return nil, false, fmt.Errorf("start claude oauth: %w", err)
	}
	if err := openBrowser(authURL); err != nil {
		// Browser open is best-effort — surface the URL via notification
		// so the user can copy it manually. The flow can still complete.
		log.Printf("menubar: open browser for claude oauth: %v", err)
		notify(
			"EveryAPI — open this URL to continue",
			authURL,
		)
	}
	notify(
		"EveryAPI — Claude auth in progress",
		"Approve in the browser, copy the code#state string, then click "+
			"\"Paste Claude auth from clipboard\" in the menu.",
	)
	return cli, true, nil
}

// completeClaudeOAuth finishes the flow against the same client
// instance returned by startClaudeOAuth. The pasted string is the
// `<code>#<state>` payload from Anthropic's callback page; the
// backend accepts that shape verbatim. Empty / whitespace-only
// clipboard returns an error so the user sees a clear "nothing
// pasted" notification rather than a backend 400.
func completeClaudeOAuth(ctx context.Context, client *api.Client, pasted string) (*api.SellerClaudeOAuthResult, error) {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return nil, errors.New("clipboard is empty — copy the code#state string from the browser first")
	}
	if len(pasted) > claudePasteMaxLen {
		return nil, fmt.Errorf("clipboard content is %d bytes — expected a short code#state from Anthropic", len(pasted))
	}
	if !claudePasteShape.MatchString(pasted) {
		return nil, errors.New("clipboard doesn't look like a code#state string — copy the value from Anthropic's callback page and try again")
	}
	res, err := client.CompleteSellerClaudeOAuth(ctx, pasted)
	if err != nil {
		return nil, fmt.Errorf("complete claude oauth: %w", err)
	}
	return res, nil
}

// runGeminiOAuth executes the full Gemini loopback flow. Blocks
// until either the listener fires, the timeout expires, or ctx is
// cancelled — intended to run on a goroutine spawned by the
// controller dispatch.
func (c *Controller) runGeminiOAuth(ctx context.Context) error {
	c.mu.Lock()
	creds := c.creds
	c.mu.Unlock()
	if creds == nil {
		return errors.New("not signed in")
	}

	name, models, ok, err := promptChannelMeta("Gemini")
	if err != nil {
		return err
	}
	if !ok {
		return nil // user canceled
	}

	listener, err := oauthloopback.Listen()
	if err != nil {
		return fmt.Errorf("loopback listen: %w", err)
	}
	defer listener.Close()

	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID).WithCookieJar()
	start, err := client.StartSellerGeminiOAuth(ctx, name, models, listener.URL())
	if err != nil {
		return fmt.Errorf("start gemini oauth: %w", err)
	}
	if err := openBrowser(start.AuthorizeURL); err != nil {
		log.Printf("menubar: open browser for gemini oauth: %v", err)
		notify("EveryAPI — open this URL to continue", start.AuthorizeURL)
	}

	waitCtx, cancel := context.WithTimeout(ctx, sellerOAuthTimeout)
	defer cancel()
	result, err := listener.Wait(waitCtx)
	if err != nil {
		return fmt.Errorf("wait for callback: %w", err)
	}
	if result.Error != "" {
		return fmt.Errorf("authorization failed: %s — %s", result.Error, result.ErrorDesc)
	}
	if result.State != start.State {
		return errors.New("authorization state mismatch (possible CSRF / stale flow)")
	}
	if result.Code == "" {
		return errors.New("authorization callback missing code")
	}

	res, err := client.CompleteSellerGeminiOAuth(ctx, result.Code, result.State)
	if err != nil {
		return fmt.Errorf("complete gemini oauth: %w", err)
	}
	notify(
		fmt.Sprintf("EveryAPI — Gemini channel #%d mounted", res.ChannelID),
		"Token expires: "+res.ExpiresAt+" (auto-refreshes before then).",
	)
	return nil
}
