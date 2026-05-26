package cmd

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Topup opens the dashboard top-up page behind the §7-5 Layer 3
// anti-phishing handshake. The shape is deliberately verbose for
// the user: print the URL, print the verification phrase, REQUIRE
// an Enter confirmation, then open the browser. The dashboard page
// will pin the same phrase at the top — if the page the user sees
// shows a different phrase (or no phrase at all), they should
// abort.
//
// The phrase is what defeats a phishing page: an attacker who
// somehow tricked the user into opening evil.com cannot reach the
// (single-use, in-memory) session in our backend, so they can't
// display the matching phrase.
func Topup(args []string) error {
	fs := flag.NewFlagSet("topup", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "skip auto-opening the browser; copy the URL by hand instead")
	if err := fs.Parse(args); err != nil {
		return err
	}

	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return err
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)

	res, err := client.CreateJumpSession(cliout.WithCtx())
	if err != nil {
		if api.IsUnauthorized(err) {
			return errors.New(i18n.T("auth.session_expired"))
		}
		return fmt.Errorf("create jump session: %w", err)
	}

	// Compose the dashboard URL ourselves. The backend deliberately
	// doesn't know about frontend routes (a frontend rename
	// shouldn't be a backend deploy), so the path lives here.
	jumpURL := fmt.Sprintf("%s/wallet?jump_session=%s",
		api.WebOriginFromBase(creds.APIBase), res.SessionID)

	cliout.Println("")
	cliout.Println(i18n.T("topup.before_open"))
	cliout.Println("")
	cliout.Printf("  %-8s %s\n", i18n.T("topup.url_label"), jumpURL)
	cliout.Printf("  %-8s %s\n", i18n.T("topup.phrase_label"), style.Bold(res.VerificationPhrase))
	if res.ExpiresIn > 0 {
		cliout.Printf("  "+i18n.T("topup.expires_in")+"\n", res.ExpiresIn)
	}
	cliout.Println("")
	cliout.Println(i18n.T("topup.checks_header"))
	cliout.Println(i18n.T("topup.check_1"))
	cliout.Println(i18n.T("topup.check_2"))
	cliout.Println(i18n.T("topup.phishing_hint"))
	cliout.Println("")

	// Confirmation gate. On a TTY this is a huh confirm prompt; off
	// TTY (CI / piped invocation) cliprompt.YesNo falls back to the
	// line-reader. EOF on a redirected stdin treats as proceed so a
	// scripted invocation can still pipe through — the phrase is on
	// stdout regardless and the caller can read it.
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
	if berr := cliprompt.OpenBrowser(jumpURL); berr == nil {
		cliout.Println(i18n.T("topup.browser_opened"))
	} else {
		fmt.Fprintln(os.Stderr, i18n.T("common.browser_open_failed"))
	}
	return nil
}
