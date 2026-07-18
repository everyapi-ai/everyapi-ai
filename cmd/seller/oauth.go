package seller

import (
	"bufio"
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

// sellerNetTimeout bounds a single upstream OAuth HTTP exchange
// (eligibility, /start, /complete) so a hung or slow backend can't
// wedge the CLI indefinitely even when the user isn't watching to
// press Ctrl+C. The interactive waits — device-code poll, loopback
// callback, stdin paste — are deliberately NOT bounded by this; they
// have their own, much longer, user-facing budgets and are
// SIGINT-cancellable via the parent signalCtx.
const sellerNetTimeout = 60 * time.Second

// sellerExchangeCtx derives a bounded child of parent for one discrete
// network round-trip. Caller MUST call the returned cancel right after
// the call returns (not deferred — these handlers make several in a
// row and we want each timer released promptly).
func sellerExchangeCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, sellerNetTimeout)
}

// sellerAddOAuth is the seller OAuth-based onboarding bridge: the CLI
// walks the seller through the provider's OAuth flow and the backend
// mounts the resulting credential as their channel — the seller
// NEVER copies a token string by hand. docs/cli/channel-marketplace.md
// §7-1 calls this the "game changer" for seller onboarding.
//
// Supported providers, each with its own flow shape:
//   - codex       — RFC 8628 device flow (type the short user_code into chatgpt.com)
//   - claude      — browser authorize + paste the code back (Anthropic pins its redirect)
//   - gemini      — loopback OAuth (local listener, no paste)
//   - antigravity — loopback OAuth (local listener); the ONLY feasible path
//     since antigravity's Google client accepts no hosted/device redirect,
//     which is why it can't be connected from the web dashboard
//
// Usage:
//
//	everyapi seller add-oauth <provider> --name <n> --models <m> [flags]
func sellerAddOAuth(args []string) error {
	if len(args) == 0 {
		return errors.New(i18n.T("seller.oauth_use_provider_help"))
	}
	provider := strings.ToLower(args[0])
	rest := args[1:]
	switch provider {
	case "help", "--help", "-h":
		cliout.Println(i18n.T("seller.oauth_usage"))
		return nil
	case "codex":
		return sellerAddOAuthCodex(rest)
	case "claude":
		return sellerAddOAuthClaude(rest)
	case "gemini":
		return sellerAddOAuthGemini(rest)
	case "antigravity":
		return sellerAddOAuthAntigravity(rest)
	case "chatgpt":
		return fmt.Errorf(i18n.T("seller.oauth_chatgpt_redirect"), provider)
	default:
		return fmt.Errorf(i18n.T("seller.oauth_unknown_provider"), provider)
	}
}

func sellerAddOAuthCodex(args []string) error {
	fs := flag.NewFlagSet("seller add-oauth codex", flag.ContinueOnError)
	name := fs.String("name", "", "channel display name (free-form, shown in the dashboard)")
	models := fs.String("models", "", "comma-separated models this channel will serve (e.g. gpt-4,gpt-4o)")
	noBrowser := fs.Bool("no-browser", false, "skip auto-opening the verification URL (you'll copy it manually)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	*name = strings.TrimSpace(*name)
	*models = strings.TrimSpace(*models)
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
	// WithCookieJar is the load-bearing call here: backend stashes the
	// device_auth_id / user_code / name / models keyed by a session
	// cookie set on /start. Without a jar the /poll call would land
	// in a fresh session and the handler would 200 with code=expired.
	client := api.ForCredentials(creds).WithCookieJar()
	ctx, stop := cliout.SignalCtx()
	defer stop()

	// Pre-check eligibility — same reasoning as `add-key`: failing a
	// gate AFTER the seller authorised in their browser is a worse UX
	// than telling them now to fix it first.
	ecx, ecancel := sellerExchangeCtx(ctx)
	elig, err := client.GetSellerEligibility(ecx)
	ecancel()
	if err != nil {
		return classifySellerErr(err)
	}
	if !elig.Eligible {
		renderEligibility(elig)
		cliout.Println("")
		cliout.Println(i18n.T("seller.eligibility_check_failed"))
		cliout.Printf(i18n.T("seller.dashboard_url_line")+"\n", api.WebOriginFromBase(creds.APIBase))
		return errors.New(i18n.T("seller.not_eligible_error"))
	}

	scx, scancel := sellerExchangeCtx(ctx)
	start, err := client.StartSellerCodexOAuth(scx, *name, *models)
	scancel()
	if err != nil {
		return classifySellerErr(err)
	}

	cliout.Println("")
	cliout.Println(i18n.T("seller.oauth_codex_intro"))
	cliout.Printf("\n    URL:  %s\n", cliout.Sanitize(start.VerificationURI))
	cliout.Printf("    Code: %s\n\n", cliout.Sanitize(start.UserCode))

	if !*noBrowser {
		// Same fail-soft approach as `everyapi auth login`: a missing
		// xdg-open / `open` shouldn't crash; the URL is already
		// printed for the user to copy.
		if berr := cliprompt.OpenBrowser(start.VerificationURI); berr == nil {
			cliout.Println(i18n.T("seller.oauth_browser_approve"))
		} else {
			fmt.Fprintln(os.Stderr, i18n.T("common.browser_open_failed"))
		}
	}
	cliout.Println("")
	cliout.Println(i18n.T("seller.oauth_waiting_auth"))

	res, err := client.PollSellerCodexUntilDone(ctx, start.FlowID, start.Interval)
	if err != nil {
		switch {
		case errors.Is(err, api.ErrSellerCodexPollExpired):
			return errors.New(i18n.T("seller.oauth_codex_timeout"))
		case errors.Is(err, api.ErrSellerCodexPollDenied):
			return errors.New(i18n.T("seller.oauth_denied"))
		case errors.Is(err, context.Canceled):
			// Ctrl+C during the poll cancels the context; exit cleanly
			// instead of dumping "Error: poll: context canceled" with a
			// non-zero status, matching `everyapi auth login`.
			cliout.Println(i18n.T("login.cancelled"))
			return nil
		default:
			return fmt.Errorf("poll: %w", classifySellerErr(err))
		}
	}

	bound := cliout.Sanitize(res.Email)
	if bound == "" {
		bound = i18n.T("seller.oauth_email_unknown")
	}
	cliout.Printf(i18n.T("seller.oauth_mounted_with_email"), res.ChannelID, *name, bound)
	cliout.Printf(i18n.T("seller.oauth_status_visit"), api.WebOriginFromBase(creds.APIBase))
	return nil
}

// sellerAddOAuthClaude drives the Anthropic Claude OAuth flow. Unlike
// codex device flow, Claude can't be auto-completed by the CLI:
// Anthropic's OAuth provider hard-codes `redirect_uri` to
// console.anthropic.com/oauth/code/callback, so we can't open a local
// listener to receive the code. The user has to paste a short string
// from the browser back into the terminal — annoying but still much
// better than chasing ~/.claude/auth.json yourself.
//
// Usage:
//
//	everyapi seller add-oauth claude --name <n> --models <m> [--no-browser]
func sellerAddOAuthClaude(args []string) error {
	fs := flag.NewFlagSet("seller add-oauth claude", flag.ContinueOnError)
	name := fs.String("name", "", "channel display name (free-form, shown in the dashboard)")
	models := fs.String("models", "", "comma-separated models this channel will serve (e.g. claude-3-opus,claude-3-sonnet)")
	noBrowser := fs.Bool("no-browser", false, "skip auto-opening the authorize URL (you'll copy it manually)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	*name = strings.TrimSpace(*name)
	*models = strings.TrimSpace(*models)
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
	// WithCookieJar same as codex: backend stashes state/verifier/
	// name/models under a session keyed by `everyapi_session` cookie,
	// and the complete call has to land in the SAME session.
	client := api.ForCredentials(creds).WithCookieJar()
	ctx, stop := cliout.SignalCtx()
	defer stop()

	// Eligibility pre-check — fail before we burn a round-trip to
	// Anthropic's authorize endpoint.
	ecx, ecancel := sellerExchangeCtx(ctx)
	elig, err := client.GetSellerEligibility(ecx)
	ecancel()
	if err != nil {
		return classifySellerErr(err)
	}
	if !elig.Eligible {
		renderEligibility(elig)
		cliout.Println("")
		cliout.Println(i18n.T("seller.eligibility_check_failed"))
		cliout.Printf(i18n.T("seller.dashboard_url_line")+"\n", api.WebOriginFromBase(creds.APIBase))
		return errors.New(i18n.T("seller.not_eligible_error"))
	}

	scx, scancel := sellerExchangeCtx(ctx)
	authorizeURL, err := client.StartSellerClaudeOAuth(scx, *name, *models)
	scancel()
	if err != nil {
		return classifySellerErr(err)
	}

	cliout.Println("")
	cliout.Println(i18n.T("seller.oauth_claude_intro"))
	cliout.Printf("\n"+i18n.T("seller.oauth_claude_step_1")+"\n", cliout.Sanitize(authorizeURL))
	cliout.Println(i18n.T("seller.oauth_claude_step_2"))
	cliout.Println(i18n.T("seller.oauth_claude_step_3a"))
	cliout.Println(i18n.T("seller.oauth_claude_step_3b"))
	cliout.Println("")

	if !*noBrowser {
		if berr := cliprompt.OpenBrowser(authorizeURL); berr == nil {
			cliout.Println(i18n.T("seller.oauth_browser_opened"))
		} else {
			fmt.Fprintln(os.Stderr, i18n.T("common.browser_open_failed"))
		}
	}

	in := bufio.NewReader(os.Stdin)
	pasted, err := cliprompt.Line(in, i18n.T("seller.oauth_paste_string"), "")
	if err != nil {
		return err
	}

	ccx, ccancel := sellerExchangeCtx(ctx)
	res, err := client.CompleteSellerClaudeOAuth(ccx, pasted)
	ccancel()
	if err != nil {
		return classifySellerErr(err)
	}

	cliout.Printf(i18n.T("seller.oauth_mounted_claude"), res.ChannelID, *name)
	if res.ExpiresAt != "" {
		cliout.Printf(i18n.T("seller.oauth_token_expires"), cliout.Sanitize(res.ExpiresAt))
	}
	cliout.Printf(i18n.T("seller.oauth_status_visit"), api.WebOriginFromBase(creds.APIBase))
	return nil
}

// sellerAddOAuthGemini is the true one-click OAuth path. Google's
// gemini-cli installed-app client accepts http://127.0.0.1:<port>/
// callback as a redirect_uri, so the CLI runs its own listener and
// Google sends the code straight to it — no manual paste like
// claude, no user_code typing like codex.
//
// Usage:
//
//	everyapi seller add-oauth gemini --name <n> --models <m> [--no-browser] [--timeout 5m]
//
// Flow:
//  1. Listen on a random ephemeral port (127.0.0.1:0)
//  2. Eligibility pre-check
//  3. Tell backend `redirect_uri = http://127.0.0.1:<port>/callback`,
//     receive the Google authorize URL + state (backend validates
//     redirect is a real loopback before handing it back)
//  4. Open browser
//  5. Block on listener until Google redirects to /callback with
//     `?code=…&state=…` OR `?error=…`
//  6. Forward code+state to backend's /complete, which exchanges
//     with Google and mints the channel
func sellerAddOAuthGemini(args []string) error {
	fs := flag.NewFlagSet("seller add-oauth gemini", flag.ContinueOnError)
	name := fs.String("name", "", "channel display name")
	models := fs.String("models", "", "comma-separated models this channel will serve (e.g. gemini-1.5-pro)")
	noBrowser := fs.Bool("no-browser", false, "skip auto-opening the authorize URL")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for the OAuth callback before giving up")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	if *timeout <= 0 {
		return fmt.Errorf("--timeout must be positive (e.g. 5m)")
	}
	*name = strings.TrimSpace(*name)
	*models = strings.TrimSpace(*models)
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
	client := api.ForCredentials(creds).WithCookieJar()

	// Start the listener BEFORE eligibility check — we need the
	// loopback URL to hand to /start. Eligibility-fail path Close()s
	// the listener immediately so the port is held for milliseconds.
	listener, err := oauthloopback.Listen()
	if err != nil {
		return fmt.Errorf("loopback listen: %w", err)
	}
	defer listener.Close()

	ctx, stop := cliout.SignalCtx()
	defer stop()

	ecx, ecancel := sellerExchangeCtx(ctx)
	elig, err := client.GetSellerEligibility(ecx)
	ecancel()
	if err != nil {
		return classifySellerErr(err)
	}
	if !elig.Eligible {
		renderEligibility(elig)
		cliout.Println("")
		cliout.Println(i18n.T("seller.eligibility_check_failed"))
		cliout.Printf(i18n.T("seller.dashboard_url_line")+"\n", api.WebOriginFromBase(creds.APIBase))
		return errors.New(i18n.T("seller.not_eligible_error"))
	}

	scx, scancel := sellerExchangeCtx(ctx)
	start, err := client.StartSellerGeminiOAuth(scx, *name, *models, listener.URL())
	scancel()
	if err != nil {
		return classifySellerErr(err)
	}

	cliout.Println("")
	cliout.Println(i18n.T("seller.oauth_gemini_intro"))
	cliout.Printf("\n    %s\n\n", cliout.Sanitize(start.AuthorizeURL))
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
		if errors.Is(err, context.Canceled) {
			// Ctrl+C while waiting for the browser callback: exit cleanly
			// instead of "Error: waiting for OAuth callback: context canceled".
			cliout.Println(i18n.T("login.cancelled"))
			return nil
		}
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
	// State check: the callback MUST carry the same state we got
	// back from /start. Otherwise this is either a stale flow
	// (browser re-played an old URL) or a forged hit. Bail with a
	// clear message — the backend also checks but failing locally
	// avoids a wasted round-trip.
	if cb.State != start.State {
		return fmt.Errorf(i18n.T("seller.oauth_state_mismatch"), cb.State, start.State)
	}

	gcx, gcancel := sellerExchangeCtx(ctx)
	res, err := client.CompleteSellerGeminiOAuth(gcx, cb.Code, cb.State)
	gcancel()
	if err != nil {
		return classifySellerErr(err)
	}

	cliout.Printf(i18n.T("seller.oauth_mounted_gemini"), res.ChannelID, *name)
	if res.ExpiresAt != "" {
		cliout.Printf(i18n.T("seller.oauth_token_expires"), cliout.Sanitize(res.ExpiresAt))
	}
	cliout.Printf(i18n.T("seller.oauth_status_visit"), api.WebOriginFromBase(creds.APIBase))
	return nil
}

// sellerAddOAuthAntigravity drives Google Antigravity's OAuth. It is the
// SAME loopback flow as gemini — antigravity's Google client only accepts
// http://127.0.0.1:<port>/callback redirects (no hosted-paste page, no
// device flow), which is exactly why this can't be done from the web
// dashboard and must run from a local listener here. The only wire
// difference from gemini is the backend endpoint, which authorizes the
// Antigravity desktop client (whose Code Assist tier serves the
// antigravity model set).
//
// Usage:
//
//	everyapi seller add-oauth antigravity --name <n> --models <m> [--no-browser] [--timeout 5m]
func sellerAddOAuthAntigravity(args []string) error {
	fs := flag.NewFlagSet("seller add-oauth antigravity", flag.ContinueOnError)
	name := fs.String("name", "", "channel display name")
	models := fs.String("models", "", "comma-separated models this channel will serve (e.g. gemini-3.1-pro-low,claude-sonnet-4-6)")
	noBrowser := fs.Bool("no-browser", false, "skip auto-opening the authorize URL")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for the OAuth callback before giving up")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	if *timeout <= 0 {
		return fmt.Errorf("--timeout must be positive (e.g. 5m)")
	}
	*name = strings.TrimSpace(*name)
	*models = strings.TrimSpace(*models)
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
	client := api.ForCredentials(creds).WithCookieJar()

	// Start the listener BEFORE eligibility — we need the loopback URL to
	// hand to /start (same ordering as gemini).
	listener, err := oauthloopback.Listen()
	if err != nil {
		return fmt.Errorf("loopback listen: %w", err)
	}
	defer listener.Close()

	ctx, stop := cliout.SignalCtx()
	defer stop()

	ecx, ecancel := sellerExchangeCtx(ctx)
	elig, err := client.GetSellerEligibility(ecx)
	ecancel()
	if err != nil {
		return classifySellerErr(err)
	}
	if !elig.Eligible {
		renderEligibility(elig)
		cliout.Println("")
		cliout.Println(i18n.T("seller.eligibility_check_failed"))
		cliout.Printf(i18n.T("seller.dashboard_url_line")+"\n", api.WebOriginFromBase(creds.APIBase))
		return errors.New(i18n.T("seller.not_eligible_error"))
	}

	scx, scancel := sellerExchangeCtx(ctx)
	start, err := client.StartSellerAntigravityOAuth(scx, *name, *models, listener.URL())
	scancel()
	if err != nil {
		return classifySellerErr(err)
	}

	cliout.Println("")
	cliout.Println(i18n.T("seller.oauth_antigravity_intro"))
	cliout.Printf("\n    %s\n\n", cliout.Sanitize(start.AuthorizeURL))
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
		if errors.Is(err, context.Canceled) {
			// Ctrl+C while waiting for the browser callback: exit cleanly
			// instead of "Error: waiting for OAuth callback: context canceled".
			cliout.Println(i18n.T("login.cancelled"))
			return nil
		}
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

	gcx, gcancel := sellerExchangeCtx(ctx)
	res, err := client.CompleteSellerAntigravityOAuth(gcx, cb.Code, cb.State)
	gcancel()
	if err != nil {
		return classifySellerErr(err)
	}

	cliout.Printf(i18n.T("seller.oauth_mounted_antigravity"), res.ChannelID, *name)
	if res.ExpiresAt != "" {
		cliout.Printf(i18n.T("seller.oauth_token_expires"), cliout.Sanitize(res.ExpiresAt))
	}
	cliout.Printf(i18n.T("seller.oauth_status_visit"), api.WebOriginFromBase(creds.APIBase))
	return nil
}
