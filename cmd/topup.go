package cmd

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Topup opens the dashboard top-up page behind the §7-5 Layer 3 anti-phishing handshake. The shape is deliberately verbose for the user: print the URL, print the verification phrase, REQUIRE an Enter confirmation, then open the browser. The dashboard page will pin the same phrase at the top — if the page the user sees shows a different phrase (or no phrase at all), they should abort.
//
// The phrase is what defeats a phishing page: an attacker who somehow tricked the user into opening evil.com cannot reach the (single-use, in-memory) session in our backend, so they can't display the matching phrase.
func Topup(args []string) error {
	fs := flag.NewFlagSet("topup", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "skip auto-opening the browser; copy the URL by hand instead")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}

	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return err
	}
	// OAuth2 relay-key logins carry no management session (no user id, the access token is a relay key), so CreateJumpSession would 401 and surface the misleading "session expired — re-login" string that re-login via the same OAuth2 path can't fix. Tell the user plainly that top-up needs a full management login instead.
	if creds.OAuthClientID != "" {
		return errors.New(i18n.T("topup.relay_key_mode"))
	}
	client := api.ForCredentials(creds)

	res, err := client.CreateJumpSession(cliout.WithCtx())
	if err != nil {
		if api.IsUnauthorized(err) {
			return errors.New(i18n.T("auth.session_expired"))
		}
		return fmt.Errorf("create jump session: %w", err)
	}

	// Compose the dashboard URL ourselves. The backend deliberately doesn't know about frontend routes (a frontend rename shouldn't be a backend deploy), so the path lives here.
	jumpURL := fmt.Sprintf("%s/wallet?jump_session=%s",
		api.WebOriginFromBase(creds.APIBase), res.SessionID)

	cliout.Println("")
	cliout.Println(i18n.T("topup.before_open"))
	cliout.Println("")
	// Both of these are gateway-controlled — the phrase and session id come straight out of the jump-session response body, the origin out of the configured api_base — and this screen's entire job is display integrity, so an unescaped CSI here would rewrite the very URL line the user is told to check (and an OSC-52 payload would write their clipboard). Sanitize first, then apply the trusted bold: the reverse ordering is what cliout.Sanitize forbids, because style.Bold's own escapes would be stripped along with the attacker's.
	cliout.Printf("  %-8s %s\n", i18n.T("topup.url_label"), cliout.Sanitize(jumpURL))
	cliout.Printf("  %-8s %s\n", i18n.T("topup.phrase_label"), style.Bold(cliout.Sanitize(res.VerificationPhrase)))
	if res.ExpiresIn > 0 {
		cliout.Printf("  "+i18n.T("topup.expires_in")+"\n", res.ExpiresIn)
	}
	cliout.Println("")
	cliout.Println(i18n.T("topup.checks_header"))
	cliout.Println(i18n.T("topup.check_1"))
	cliout.Println(i18n.T("topup.check_2"))
	cliout.Println(i18n.T("topup.phishing_hint"))
	cliout.Println("")

	// Confirmation gate. On a TTY this is a huh confirm prompt; off TTY (CI / piped invocation) cliprompt.YesNo falls back to the line-reader. EOF on a redirected stdin treats as proceed so a scripted invocation can still pipe through — the phrase is on stdout regardless and the caller can read it.
	confirmed, cErr := cliprompt.YesNo(bufio.NewReader(os.Stdin), i18n.T("common.open_browser_now"), true)
	if cErr != nil {
		if errors.Is(cErr, io.EOF) {
			confirmed = true
		} else {
			return fmt.Errorf("read confirmation: %w", cErr)
		}
	}
	if !confirmed {
		return errors.New(i18n.T("common.aborted_by_user"))
	}

	cliout.Println("")
	if *noBrowser {
		cliout.Println(i18n.T("topup.copy_url"))
		return nil
	}
	// Deliberately the unsanitized jumpURL: the launcher is not a terminal sink, and cliprompt.IsBrowsableURL already refuses anything that is not a plain absolute http(s) URL. Handing it a sanitized copy would silently open a different address than the one the session belongs to.
	if berr := cliprompt.OpenBrowser(jumpURL); berr == nil {
		cliout.Println(i18n.T("topup.browser_opened"))
	} else {
		fmt.Fprintln(os.Stderr, i18n.T("common.browser_open_failed"))
	}
	return nil
}
