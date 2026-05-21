package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Login runs the device authorization flow: POST start → show user
// the QR code + code + URL → user scans on phone OR opens browser →
// poll until authorized → write credentials.
//
// The QR encodes the verification URL with `?code=` pre-filled, so a
// phone scan lands on the confirm page with the code already in the
// input — no retyping the 8-character string on a tiny keyboard.
// docs/cli/channel-marketplace.md §7-5 Layer 1 (device-to-device
// QR sign-in) realised here, on top of the existing device-auth
// backend that #133 shipped.
//
// Flags:
//
//	--api-base <url>  override default https://api.everyapi.ai (dev / self-host)
//	--no-browser      skip the auto-open; user copies the URL manually
//	--no-qr           skip the terminal QR (for non-UTF-8 terminals / pipes)
func Login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	apiBase := fs.String("api-base", config.DefaultAPIBase, "EveryAPI API base URL")
	noBrowser := fs.Bool("no-browser", false, "skip opening the browser automatically")
	noQR := fs.Bool("no-qr", false, "skip rendering the QR code (useful for non-UTF-8 terminals or when piping output)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	*apiBase = strings.TrimRight(*apiBase, "/")

	client := api.New(*apiBase, "")
	// signalCtx (not withCtx): the device-auth poll below blocks for
	// minutes. The "(Ctrl+C to cancel)" line we print must be true —
	// cancel the in-flight poll on SIGINT instead of hard-killing.
	ctx, stop := cliout.SignalCtx()
	defer stop()

	start, err := client.DeviceAuthStart(ctx)
	if err != nil {
		return fmt.Errorf("start device authorization: %w", err)
	}

	// URL with code pre-filled so a phone QR scan lands on the
	// dashboard confirm page with the input already populated. The
	// fallback "type the code by hand" path still works against the
	// bare verification_uri printed below.
	prefilledURL := buildVerificationURLWithCode(start.VerificationURI, start.UserCode)

	cliout.Println("")
	if !*noQR {
		cliout.Println("Scan this QR with your phone (or any device already signed in to EveryAPI):")
		cliout.Println("")
		// qrterminal renders to stdout with Unicode half-blocks by
		// default (▀▄ etc.) — about half the height of the ASCII
		// "▓▓" form. Level L recovery is fine for short URLs and
		// keeps the QR small enough to fit a normal terminal.
		qrterminal.GenerateHalfBlock(prefilledURL, qrterminal.L, cliout.Out)
		cliout.Println("")
		cliout.Println("Or visit this URL manually (code is baked into the link):")
	} else {
		cliout.Println("To authorize this device, visit (code is baked into the link):")
	}
	cliout.Printf("\n    %s\n\n", prefilledURL)
	// Surface the bare user_code too in case the dashboard fails to
	// pre-fill (older /cli/auth deploys, query-stripping middlebox,
	// user pasted the URL into a tool that drops query strings).
	cliout.Printf("If the page doesn't pre-fill, enter the code: %s\n\n", start.UserCode)

	if !*noBrowser {
		if err := cliprompt.OpenBrowser(prefilledURL); err == nil {
			cliout.Println("Browser opened. Approve there (or finish on your phone); this will finish on its own.")
		} else {
			// stderr so a user piping `everyapi login | …` gets a clean
			// stdout (the URL + code go through the cmd.Out writer
			// above). xdg-open missing on a headless Linux desktop is
			// the common case here.
			fmt.Fprintln(os.Stderr, "Couldn't open the browser automatically — scan the QR or copy the URL above.")
		}
	}

	// Probe stdin once so we pick the right hint copy AND only bother
	// starting the raw-mode watcher when keystrokes are reachable.
	fd := int(os.Stdin.Fd())
	ttyIn := term.IsTerminal(fd)

	cliout.Println("")
	if ttyIn {
		cliout.Println("Waiting for authorization... (Ctrl+C to cancel, press 'c' to copy URL)")
	} else {
		cliout.Println("Waiting for authorization... (Ctrl+C to cancel)")
	}

	// Wrap ctx in WithCancel so the raw-mode reader (which swallows
	// SIGINT — see startLoginKeyWatcher) can still propagate Ctrl+C
	// as a context cancellation. Outside raw mode the existing
	// SignalCtx already cancels ctx on SIGINT, so this is a no-op
	// passthrough then.
	pctx, cancelPoll := context.WithCancel(ctx)
	defer cancelPoll()

	stopWatcher := func() {}
	if ttyIn {
		stopWatcher = startLoginKeyWatcher(fd, prefilledURL, cancelPoll)
	}
	defer stopWatcher()

	res, err := client.PollUntilDone(pctx, start.DeviceCode, start.Interval)
	// Restore terminal BEFORE further printing — the success / error
	// branches below use plain "\n", which renders as a column-zero
	// newline only in cooked mode. stopWatcher is idempotent so the
	// deferred call is harmless.
	stopWatcher()
	if err != nil {
		switch err {
		case api.ErrDeviceAuthExpired:
			return fmt.Errorf("the code timed out before you authorized — run 'everyapi login' again")
		case api.ErrDeviceAuthDenied:
			return fmt.Errorf("authorization was denied in the browser")
		default:
			return fmt.Errorf("poll: %w", err)
		}
	}

	creds := &config.Credentials{
		APIBase:     *apiBase,
		AccessToken: res.AccessToken,
		UserID:      res.UserID,
		Username:    res.Username,
	}
	// One extra GetSelf round-trip during login to capture the
	// user's backend role (admin / common). We persist it so the
	// help-text renderer can hide admin-only subcommands locally
	// without per-help-render network traffic. A failure here is
	// non-fatal — login itself succeeded; role defaults to 0
	// (treated as non-admin), and the next `everyapi status` will
	// retry the lookup.
	//
	// Reuse the SignalCtx so Ctrl+C still cancels (the device-auth
	// poll just returned, but a stuck TCP connection here would
	// otherwise wedge the login flow indefinitely). 10s cap because
	// the device-auth happy path completes in <1s; anything longer
	// is a transient backend issue worth giving up on rather than
	// blocking the user.
	roleCtx, roleCancel := context.WithTimeout(ctx, 10*time.Second)
	if self, sErr := api.New(*apiBase, res.AccessToken).
		WithUserID(res.UserID).
		GetSelf(roleCtx); sErr == nil {
		creds.Role = self.Role
	}
	roleCancel()
	if err := config.Save(creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	dir, _ := config.ConfigDir()
	cliout.Printf("\nLogged in as %s. Credentials saved to %s/credentials.json\n", res.Username, dir)

	// Resolve the relay API key now (and cache it) so `everyapi use`
	// works on first try. The access token alone can't relay — it's
	// a management credential — so without this step `use` would
	// 401.
	//
	// A failure here is non-fatal: login itself already succeeded and
	// credentials are saved. We only surface the errNoRelayKey
	// sentinel (account has zero enabled keys — an actionable state
	// the user must fix in the dashboard). Other failures (transient
	// 5xx, network blip) are SWALLOWED: after a successful device-auth
	// flow, a noisy "warning: ..." line on stderr would make the user
	// doubt the login itself, and the next `everyapi use` / `everyapi
	// status` will retry the resolution anyway.
	if _, err := resolveRelayKey(creds, ""); err != nil && errors.Is(err, errNoRelayKey) {
		cliout.Println("")
		cliout.Println("Note: your account has no relay API key yet. `everyapi use` needs one")
		cliout.Println("(it's separate from this login token). Create an API key in the")
		cliout.Println("EveryAPI dashboard, then run 'everyapi login' again.")
	}

	cliout.Println("Next: try 'everyapi status' or 'everyapi use claude'.")
	return nil
}

// startLoginKeyWatcher puts stdin into raw mode for the duration of
// the device-auth poll and watches for single keystrokes:
//
//	c / C   — copy the prefilled verification URL to the clipboard
//	^C / ^D — cancel the poll (raw mode swallows SIGINT, so we have
//	          to propagate cancellation through ctx ourselves)
//
// Returns an idempotent stop func that restores the terminal. Caller
// must invoke it before any subsequent printing — in raw mode "\n"
// alone leaves the cursor mid-line and the success message would
// look like staircase output otherwise.
//
// Robustness notes: when MakeRaw fails (rare — non-tty fd, locked-
// down container) we return a no-op so the rest of login still works;
// the URL is already on screen for manual copy. The reader goroutine
// outlives stop() in the edge case where it's still blocked on
// os.Stdin.Read — acceptable because login is a short-lived CLI
// command and the OS reaps the goroutine on process exit; the
// `stopped` flag stops it from acting on any keystroke that sneaks
// in between Restore and process exit.
func startLoginKeyWatcher(fd int, url string, cancelPoll context.CancelFunc) func() {
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return func() {}
	}

	var stopped atomic.Bool
	stop := func() {
		if stopped.CompareAndSwap(false, true) {
			_ = term.Restore(fd, oldState)
		}
	}

	go func() {
		buf := make([]byte, 1)
		for {
			n, rerr := os.Stdin.Read(buf)
			if rerr != nil || n == 0 || stopped.Load() {
				return
			}
			switch buf[0] {
			case 'c', 'C':
				// Raw mode means a bare "\n" stays in the same column.
				// Use "\r\n" so the message lines up at column zero.
				if cerr := cliprompt.CopyToClipboard(url); cerr == nil {
					fmt.Fprint(cliout.Out, "\r\nURL copied to clipboard.\r\n")
				} else {
					fmt.Fprintf(cliout.Out, "\r\nCouldn't copy: %v\r\n", cerr)
				}
			case 0x03, 0x04: // ^C, ^D
				cancelPoll()
				return
			}
		}
	}()
	return stop
}
