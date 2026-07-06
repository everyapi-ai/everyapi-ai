package admin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/everyapi-ai/everyapi-sdk/oauthloopback"
)

// adminChannelAddOAuth is the operator counterpart of `seller add-oauth`:
// it mounts a platform-operated OAuth channel (OwnerUserID 0, operator
// group, no marketplace gating) rather than a seller channel. Only
// antigravity is wired — codex/claude/gemini already have an admin
// connect path in the dashboard, but antigravity's Google client accepts
// only a loopback redirect (no hosted-paste / device flow), so the
// dashboard can't drive it and the CLI's local listener is the only way.
func adminChannelAddOAuth(args []string) error {
	if len(args) == 0 {
		return errors.New(i18n.T("admin.channel.oauth_usage"))
	}
	provider := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]
	switch provider {
	case "antigravity":
		return adminChannelAddOAuthAntigravity(rest)
	default:
		return fmt.Errorf(i18n.T("admin.channel.oauth_unknown_provider"), provider)
	}
}

// adminChannelAddOAuthAntigravity drives Google Antigravity's loopback OAuth
// and mounts an operator channel. Same listener dance as the seller flow
// (cmd/seller/oauth.go) — the only differences are the backend endpoint
// (/api/channel/antigravity/oauth/*, channels:write), an operator --group,
// and no seller-eligibility pre-check.
//
// Usage:
//
//	everyapi admin channel add-oauth antigravity --name <n> --models <m> [--group <g>] [--no-browser] [--timeout 5m]
func adminChannelAddOAuthAntigravity(args []string) error {
	fs := flag.NewFlagSet("admin channel add-oauth antigravity", flag.ContinueOnError)
	name := fs.String("name", "", "channel display name")
	models := fs.String("models", "", "comma-separated models this channel will serve (e.g. gemini-3.1-pro-low,claude-sonnet-4-6)")
	group := fs.String("group", "default", "token group this channel serves")
	noBrowser := fs.Bool("no-browser", false, "skip auto-opening the authorize URL")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for the OAuth callback before giving up")
	if err := fs.Parse(args); err != nil {
		return err
	}
	*name = strings.TrimSpace(*name)
	*models = strings.TrimSpace(*models)
	*group = strings.TrimSpace(*group)
	if *group == "" {
		*group = "default"
	}
	if *name == "" || *models == "" {
		var missing []string
		if *name == "" {
			missing = append(missing, "--name")
		}
		if *models == "" {
			missing = append(missing, "--models")
		}
		return fmt.Errorf(i18n.T("seller.oauth_missing_flags"), strings.Join(missing, ", "))
	}

	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return err
	}
	// CookieJar is required: the backend stashes the OAuth flow state in a
	// session keyed by the everyapi_session cookie across start→complete.
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID).WithCookieJar()

	listener, err := oauthloopback.Listen()
	if err != nil {
		return fmt.Errorf("loopback listen: %w", err)
	}
	defer listener.Close()

	ctx, stop := cliout.SignalCtx()
	defer stop()

	scx, scancel := context.WithTimeout(ctx, 30*time.Second)
	start, err := client.StartAdminAntigravityOAuth(scx, *name, *models, *group, listener.URL())
	scancel()
	if err != nil {
		return classifyErr(err)
	}

	cliout.Println("")
	cliout.Println(i18n.T("seller.oauth_antigravity_intro"))
	cliout.Printf("\n    %s\n\n", start.AuthorizeURL)
	if !*noBrowser {
		if berr := cliprompt.OpenBrowser(start.AuthorizeURL); berr == nil {
			cliout.Println(i18n.T("seller.oauth_browser_sign_in"))
		} else {
			fmt.Fprintln(os.Stderr, i18n.T("common.browser_open_failed"))
		}
	}
	cliout.Printf(i18n.T("seller.oauth_waiting_redirect"), listener.Port(), *timeout)

	waitCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cb, err := listener.Wait(waitCtx)
	if err != nil {
		return fmt.Errorf("waiting for OAuth callback: %w", err)
	}
	if cb.Error != "" {
		desc := cb.ErrorDesc
		if desc == "" {
			desc = cb.Error
		}
		return fmt.Errorf("authorization failed: %s", cliout.Sanitize(desc))
	}
	if cb.Code == "" {
		return errors.New(i18n.T("seller.oauth_callback_no_code"))
	}
	if cb.State != start.State {
		return fmt.Errorf(i18n.T("seller.oauth_state_mismatch"), cb.State, start.State)
	}

	gcx, gcancel := context.WithTimeout(ctx, 30*time.Second)
	res, err := client.CompleteAdminAntigravityOAuth(gcx, cb.Code, cb.State)
	gcancel()
	if err != nil {
		return classifyErr(err)
	}

	cliout.Printf(i18n.T("admin.channel.oauth_mounted"), res.ChannelID, *name, *group)
	if res.ExpiresAt != "" {
		cliout.Printf(i18n.T("seller.oauth_token_expires"), res.ExpiresAt)
	}
	cliout.Printf(i18n.T("admin.channel.oauth_verify_hint"), res.ChannelID)
	return nil
}
