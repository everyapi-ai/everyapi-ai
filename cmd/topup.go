package cmd

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
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
		return errors.New("not logged in — run 'everyapi login' first")
	}
	if err != nil {
		return err
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)

	res, err := client.CreateJumpSession(cliout.WithCtx())
	if err != nil {
		if api.IsUnauthorized(err) {
			return errors.New("your session expired — run 'everyapi login' again")
		}
		return fmt.Errorf("create jump session: %w", err)
	}

	// Compose the dashboard URL ourselves. The backend deliberately
	// doesn't know about frontend routes (a frontend rename
	// shouldn't be a backend deploy), so the path lives here.
	jumpURL := fmt.Sprintf("%s/wallet?jump_session=%s",
		api.WebOriginFromBase(creds.APIBase), res.SessionID)

	cliout.Println("")
	cliout.Println("Before opening the browser, verify this is the real EveryAPI dashboard.")
	cliout.Println("")
	cliout.Printf("  URL:    %s\n", jumpURL)
	cliout.Printf("  Phrase: %s\n", res.VerificationPhrase)
	if res.ExpiresIn > 0 {
		cliout.Printf("  (this session expires in ~%d seconds)\n", res.ExpiresIn)
	}
	cliout.Println("")
	cliout.Println("Check TWO things after the browser opens:")
	cliout.Println("  1) The page URL above must be on YOUR EveryAPI origin")
	cliout.Println("     (app.everyapi.ai by default, or your self-host).")
	cliout.Println("  2) The page header must show the SAME phrase above.")
	cliout.Println("If either differs, close the tab — you may be looking at a phishing page.")
	cliout.Println("")

	// Confirmation gate. On a TTY this is a huh confirm prompt; off
	// TTY (CI / piped invocation) cliprompt.YesNo falls back to the
	// line-reader. EOF on a redirected stdin treats as proceed so a
	// scripted invocation can still pipe through — the phrase is on
	// stdout regardless and the caller can read it.
	confirmed, cErr := cliprompt.YesNo(bufio.NewReader(os.Stdin), "Open the browser now?", true)
	if cErr != nil {
		if errors.Is(cErr, io.EOF) {
			confirmed = true
		} else {
			return fmt.Errorf("read confirmation: %w", cErr)
		}
	}
	if !confirmed {
		return errors.New("aborted by user")
	}

	cliout.Println("")
	if *noBrowser {
		cliout.Println("Copy the URL above into your browser.")
		return nil
	}
	if berr := cliprompt.OpenBrowser(jumpURL); berr == nil {
		cliout.Println("Browser opened. Verify the phrase on the page matches the one above.")
	} else {
		fmt.Fprintln(os.Stderr, "Couldn't open the browser automatically — copy the URL above.")
	}
	return nil
}
