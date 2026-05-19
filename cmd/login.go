package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mdp/qrterminal/v3"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
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
	ctx, stop := signalCtx()
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

	println("")
	if !*noQR {
		println("Scan this QR with your phone (or any device already signed in to EveryAPI):")
		println("")
		// qrterminal renders to stdout with Unicode half-blocks by
		// default (▀▄ etc.) — about half the height of the ASCII
		// "▓▓" form. Level L recovery is fine for short URLs and
		// keeps the QR small enough to fit a normal terminal.
		qrterminal.GenerateHalfBlock(prefilledURL, qrterminal.L, Out)
		println("")
		println("Or visit this URL manually:")
	} else {
		println("To authorize this device, visit:")
	}
	printf("\n    %s\n\n", start.VerificationURI)
	println("And enter the code:")
	printf("\n    %s\n\n", start.UserCode)

	if !*noBrowser {
		if err := openBrowser(prefilledURL); err == nil {
			println("Browser opened. Approve there (or finish on your phone); this will finish on its own.")
		} else {
			// stderr so a user piping `everyapi login | …` gets a clean
			// stdout (the URL + code go through the cmd.Out writer
			// above). xdg-open missing on a headless Linux desktop is
			// the common case here.
			fmt.Fprintln(os.Stderr, "Couldn't open the browser automatically — scan the QR or copy the URL above.")
		}
	}
	println("")
	println("Waiting for authorization... (Ctrl+C to cancel)")

	res, err := client.PollUntilDone(ctx, start.DeviceCode, start.Interval)
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
	if err := config.Save(creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	dir, _ := config.ConfigDir()
	printf("\nLogged in as %s. Credentials saved to %s/credentials.json\n", res.Username, dir)

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
		println("")
		println("Note: your account has no relay API key yet. `everyapi use` needs one")
		println("(it's separate from this login token). Create an API key in the")
		println("EveryAPI dashboard, then run 'everyapi login' again.")
	}

	println("Next: try 'everyapi status' or 'everyapi use claude'.")
	return nil
}

// openBrowser tries the platform's standard "open URL" helper. We
// intentionally use exec.Command + ignore stderr: a browser launcher
// that fails should not look like a CLI bug — we already print the URL
// for the user to copy.
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}
