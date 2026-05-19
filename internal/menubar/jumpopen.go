package menubar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
)

// errUntrustedJumpURL is what jump-session validation returns when
// the backend hands us a URL whose origin doesn't match the dashboard
// we expect. Logged + notified; never dispatched to the browser.
var errUntrustedJumpURL = errors.New("jump-session URL host doesn't match the dashboard origin — refusing to open")

// validateJumpURL parses `raw` and rejects it unless:
//   - the URL parses cleanly,
//   - scheme is one of {"https", "http"} (http only when the dashboard
//     origin is itself http — covers self-host / dev),
//   - host matches the dashboard origin derived from creds.APIBase.
//
// Without this check, a compromised backend / hostile self-host /
// MITM on a self-host base could swap in `javascript:` / `file:` /
// `vbscript:` / a totally different domain, and openBrowser would
// happily dispatch it (especially on Windows where `rundll32
// url.dll,FileProtocolHandler` runs arbitrary protocol handlers).
func validateJumpURL(raw, dashboardOrigin string) error {
	if raw == "" {
		return errors.New("jump-session URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("jump-session URL parse: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("%w (scheme=%q)", errUntrustedJumpURL, u.Scheme)
	}
	dash, err := url.Parse(dashboardOrigin)
	if err != nil {
		return fmt.Errorf("dashboard origin parse: %w", err)
	}
	// http→https mismatch is fine in one direction (dashboard https,
	// API base http would be weird but not attacker-controlled).
	// Reject when the URL is http but the dashboard expects https —
	// otherwise an attacker could downgrade.
	if dash.Scheme == "https" && u.Scheme != "https" {
		return fmt.Errorf("%w (downgrade from https to http)", errUntrustedJumpURL)
	}
	if !strings.EqualFold(u.Host, dash.Host) {
		return fmt.Errorf("%w (host=%q, expected=%q)", errUntrustedJumpURL, u.Host, dash.Host)
	}
	return nil
}

// openViaJumpPhrase is the menubar half of §4.7-7-5 Layer 3
// (anti-phishing jump-phrase). The CLI prints the phrase to the
// terminal and waits on Enter; the menubar surfaces a native modal
// instead — the user must literally read the phrase before the
// browser opens. Same security primitive, friendlier shape.
//
// intent must be one of the backend-approved values: "topup",
// "wallet", "channels". Anything else returns the backend's 400.
func (c *Controller) openViaJumpPhrase(intent, dialogTitle string) {
	c.mu.Lock()
	creds := c.creds
	c.mu.Unlock()
	if creds == nil {
		log.Printf("menubar: openViaJumpPhrase intent=%s: not signed in", intent)
		return
	}

	ctx, cancel := context.WithTimeout(c.shutdownCtx, 15*time.Second)
	defer cancel()
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	res, err := client.CreateJumpSession(ctx, intent)
	if err != nil {
		if api.IsUnauthorized(err) {
			log.Printf("menubar: jump-session %s: 401, dropping session", intent)
			select {
			case c.kick <- cmdSignOut:
			default:
			}
			return
		}
		log.Printf("menubar: create jump-session %s: %v", intent, err)
		return
	}

	body := fmt.Sprintf(
		"Verify this phrase BEFORE opening the browser:\n\n%s\n\n"+
			"After the browser opens, the dashboard page should show the SAME phrase at the top.\n"+
			"If it doesn't, close the tab — the page may be phishing.\n\n"+
			"This session expires in ~%d seconds.",
		res.VerificationPhrase, res.ExpiresIn,
	)
	confirmed, err := confirmDialog(dialogTitle, body, "Open Browser", "Cancel")
	if err != nil {
		// fail-closed: the anti-phishing modal MUST fire before the
		// browser opens. If the host can't render one, surface the
		// reason and don't dispatch the URL.
		log.Printf("menubar: confirm dialog: %v", err)
		notify("EveryAPI — phishing-check modal not available",
			err.Error()+" — install zenity or kdialog, then retry.")
		return
	}
	if !confirmed {
		return
	}
	dashboardOrigin := api.WebOriginFromBase(creds.APIBase)
	if err := validateJumpURL(res.URL, dashboardOrigin); err != nil {
		log.Printf("menubar: %v", err)
		notify("EveryAPI — refusing to open suspicious URL", err.Error())
		return
	}
	if err := openBrowser(res.URL); err != nil {
		log.Printf("menubar: open browser: %v", err)
	}
}
