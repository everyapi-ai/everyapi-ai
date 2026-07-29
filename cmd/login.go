package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
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
//	--api-base <url>  override configured/default gateway (dev / self-host)
//	--no-browser      skip the auto-open; user copies the URL manually
//	--no-qr           skip the terminal QR (for non-UTF-8 terminals / pipes)
func Login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	apiBase := fs.String("api-base", "", "EveryAPI API base URL")
	noBrowser := fs.Bool("no-browser", false, "skip opening the browser automatically")
	noQR := fs.Bool("no-qr", false, "skip rendering the QR code (useful for non-UTF-8 terminals or when piping output)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectFlagPositionals(fs); err != nil {
		return err
	}
	unlock, err := acquireCredentialLock()
	if err != nil {
		return fmt.Errorf("lock credential cache: %w", err)
	}
	defer unlock()
	resolvedAPIBase, err := resolveLoginAPIBase(*apiBase)
	if err != nil {
		if errors.Is(err, cliprompt.ErrPickCancelled) {
			cliout.Println(i18n.T("common.canceled"))
			return nil
		}
		return err
	}

	client := api.New(resolvedAPIBase, "")
	// signalCtx (not withCtx): the device-auth poll below blocks for
	// minutes. The "(Ctrl+C to cancel)" line we print must be true —
	// cancel the in-flight poll on SIGINT instead of hard-killing.
	ctx, stop := cliout.SignalCtx()
	defer stop()

	start, oauth2, err := startDeviceFlow(ctx, client)
	if err != nil {
		return fmt.Errorf("start device authorization: %w", err)
	}

	// URL with code pre-filled so a phone QR scan lands on the
	// dashboard confirm page with the input already populated. The
	// fallback "type the code by hand" path still works against the
	// bare verification_uri printed below.
	prefilledURL := buildVerificationURLWithCode(start.VerificationURI, start.UserCode)
	// The verification_uri is server-controlled. Only feed a well-formed
	// http(s) URL to the QR renderer, clipboard, and browser launcher — a
	// value carrying control bytes or a leading '-' would drive the
	// terminal (OSC/CSI injection) or inject options into open/xdg-open.
	// The printed URL text always goes through Sanitize regardless.
	safeURL := ""
	if isDisplayableURL(prefilledURL) {
		safeURL = prefilledURL
	}

	cliout.Println("")
	if !*noQR && safeURL != "" {
		cliout.Println(i18n.T("login.qr_hint"))
		cliout.Println("")
		// qrterminal renders to stdout with Unicode half-blocks by
		// default (▀▄ etc.) — about half the height of the ASCII
		// "▓▓" form. Level L recovery is fine for short URLs and
		// keeps the QR small enough to fit a normal terminal.
		qrterminal.GenerateHalfBlock(safeURL, qrterminal.L, cliout.Out)
		cliout.Println("")
		cliout.Println(i18n.T("login.url_hint_with_qr"))
	} else {
		cliout.Println(i18n.T("login.url_hint"))
	}
	cliout.Printf("\n    %s\n\n", cliout.Sanitize(prefilledURL))
	// Surface the bare user_code too in case the dashboard fails to
	// pre-fill (older /cli/auth deploys, query-stripping middlebox,
	// user pasted the URL into a tool that drops query strings).
	cliout.Printf(i18n.T("login.code_hint")+"\n\n", style.Bold(cliout.Sanitize(start.UserCode)))

	if !*noBrowser && safeURL != "" {
		if err := cliprompt.OpenBrowser(safeURL); err == nil {
			cliout.Println(i18n.T("login.browser_opened"))
		} else {
			// stderr so a user piping `everyapi auth login | …` gets a clean
			// stdout (the URL + code go through the cmd.Out writer
			// above). xdg-open missing on a headless Linux desktop is
			// the common case here.
			fmt.Fprintln(os.Stderr, i18n.T("common.browser_open_failed_qr"))
		}
	}

	// Probe stdin once so we pick the right hint copy AND only bother
	// starting the raw-mode watcher when keystrokes are reachable.
	fd := int(os.Stdin.Fd())
	ttyIn := term.IsTerminal(fd)

	cliout.Println("")
	// Only advertise the 'c'-to-copy hint when there's a validated URL to
	// copy. If the verification_uri failed validation (safeURL==""), the
	// key watcher ignores 'c', so promising the copy would be a dead
	// control — show the plain waiting line instead.
	if ttyIn && safeURL != "" {
		cliout.Println(i18n.T("login.waiting_with_copy"))
	} else {
		cliout.Println(i18n.T("login.waiting"))
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
		// Pass safeURL: the 'c' keystroke copies it to the clipboard, so
		// an untrusted/malformed verification_uri must not be copyable.
		stopWatcher = startLoginKeyWatcher(fd, safeURL, cancelPoll)
	}
	defer stopWatcher()

	// OAuth2 fallback path: the access token is itself the relay key, so it's
	// saved directly with no management session / relay-key resolution.
	if oauth2 {
		return finishOAuth2Login(pctx, resolvedAPIBase, client, start, stopWatcher)
	}

	res, err := client.PollUntilDone(pctx, start.DeviceCode, start.Interval)
	// Restore terminal BEFORE further printing — the success / error
	// branches below use plain "\n", which renders as a column-zero
	// newline only in cooked mode. stopWatcher is idempotent so the
	// deferred call is harmless.
	stopWatcher()
	if err != nil {
		switch err {
		case api.ErrDeviceAuthExpired:
			return fmt.Errorf("the code timed out before you authorized — run 'everyapi auth login' again")
		case api.ErrDeviceAuthDenied:
			return fmt.Errorf("authorization was denied in the browser")
		default:
			if errors.Is(err, context.Canceled) {
				// Ctrl+C during the poll cancels the context; the UI told the
				// user "Ctrl+C to cancel", so exit cleanly instead of dumping
				// "Error: poll: context canceled" with a non-zero status.
				cliout.Println(i18n.T("login.cancelled"))
				return nil
			}
			return fmt.Errorf("poll: %w", err)
		}
	}

	creds := &config.Credentials{
		APIBase:     resolvedAPIBase,
		AccessToken: res.AccessToken,
		UserID:      res.UserID,
		Username:    res.Username,
	}
	// One extra GetSelf round-trip during login to capture the
	// user's backend role (admin / common). We persist it so the
	// help-text renderer can hide admin-only subcommands locally
	// without per-help-render network traffic. A failure here is
	// non-fatal — login itself succeeded; role defaults to 0
	// (treated as non-admin), and the next `everyapi auth status` will
	// retry the lookup.
	//
	// Reuse the SignalCtx so Ctrl+C still cancels (the device-auth
	// poll just returned, but a stuck TCP connection here would
	// otherwise wedge the login flow indefinitely). 10s cap because
	// the device-auth happy path completes in <1s; anything longer
	// is a transient backend issue worth giving up on rather than
	// blocking the user.
	roleCtx, roleCancel := context.WithTimeout(ctx, 10*time.Second)
	if self, sErr := api.New(resolvedAPIBase, res.AccessToken).
		WithUserID(res.UserID).
		GetSelf(roleCtx); sErr == nil {
		creds.Role = self.Role
	}
	roleCancel()
	if err := config.Save(creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	dir, _ := config.ConfigDir()
	cliout.Printf(i18n.T("login.logged_in_saved"), style.Bold(cliout.Sanitize(res.Username)), dir)

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
	if _, err := resolveRelayKeyLocked(creds, ""); err != nil && errors.Is(err, errNoRelayKey) {
		cliout.Println("")
		cliout.Println(i18n.T("login.no_relay_note_1"))
		cliout.Println("(it's separate from this login token). Create an API key in the")
		cliout.Println(i18n.T("login.no_relay_note_2"))
	}

	cliout.Println(i18n.T("login.next_hint"))
	return nil
}

type gatewayRegionPicker func(prompt string, items []string, initial int) (int, error)

func resolveLoginAPIBase(apiBaseOverride string) (string, error) {
	if strings.TrimSpace(apiBaseOverride) != "" {
		return config.ResolveAPIBase(apiBaseOverride), nil
	}
	if err := ensureGatewayRegionPreference(cliprompt.IsInteractive, cliprompt.PickWithSelected); err != nil {
		return "", err
	}
	return config.ResolveAPIBase(""), nil
}

func ensureGatewayRegionPreference(isInteractive func() bool, pick gatewayRegionPicker) error {
	s, err := config.LoadSettings()
	if err != nil {
		return err
	}
	if strings.TrimSpace(s.GatewayRegion) != "" || !isInteractive() {
		return nil
	}

	choices := []string{
		i18n.T("login.gateway_region_global"),
		i18n.T("login.gateway_region_cn"),
	}
	idx, err := pick(i18n.T("login.gateway_region_prompt"), choices, 0)
	if err != nil {
		return err
	}
	if idx == 1 {
		s.GatewayRegion = "cn"
	} else {
		s.GatewayRegion = "global"
	}
	return config.SaveSettings(s)
}

// oauth2CLIClientID is the CLI's first-party OAuth2 client id (seeded in the
// backend). Used only on the fallback path.
const oauth2CLIClientID = "everyapi-cli"

// startDeviceFlow begins device authorization, preferring the legacy
// /api/cli/device-auth-* flow — which yields a management session the CLI's
// status/token commands rely on — and falling back to the OAuth2 device grant
// only when the legacy endpoint is absent (404). The bool reports whether the
// OAuth2 flow was used.
func startDeviceFlow(ctx context.Context, client *api.Client) (*api.DeviceAuthStartResp, bool, error) {
	start, err := client.DeviceAuthStart(ctx)
	if err == nil {
		return start, false, nil
	}
	var ae *api.APIError
	if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
		oStart, oErr := client.OAuth2DeviceStart(ctx, oauth2CLIClientID)
		if oErr == nil {
			return oStart, true, nil
		}
		if !errors.Is(oErr, api.ErrOAuth2Unavailable) {
			return nil, false, oErr
		}
		// OAuth2 also unavailable → fall through to the original legacy error.
	}
	return nil, false, err
}

// finishOAuth2Login completes the OAuth2 device flow. The issued access token
// is itself a relay key (sk-everyapi-…), so it's stored as both the relay key
// and the access token — the CLI is "logged in" and `everyapi use` relays.
// There is no management session in this mode, so role lookup, status, and
// token-admin commands are limited.
func finishOAuth2Login(ctx context.Context, apiBase string, client *api.Client, start *api.DeviceAuthStartResp, stopWatcher func()) error {
	tok, err := client.OAuth2PollUntilDone(ctx, oauth2CLIClientID, start.DeviceCode, start.Interval)
	stopWatcher()
	if err != nil {
		switch err {
		case api.ErrDeviceAuthExpired:
			return fmt.Errorf("the code timed out before you authorized — run 'everyapi auth login' again")
		case api.ErrDeviceAuthDenied:
			return fmt.Errorf("authorization was denied in the browser")
		default:
			if errors.Is(err, context.Canceled) {
				// Ctrl+C during the poll cancels the context; the UI told the
				// user "Ctrl+C to cancel", so exit cleanly instead of dumping
				// "Error: poll: context canceled" with a non-zero status.
				cliout.Println(i18n.T("login.cancelled"))
				return nil
			}
			return fmt.Errorf("poll: %w", err)
		}
	}
	// The access token is itself the relay key; keep the refresh token + expiry
	// so ResolveRelayKey can renew it before the 90-day key lapses.
	creds := &config.Credentials{
		APIBase:           apiBase,
		AccessToken:       tok.AccessToken,
		RelayKey:          tok.AccessToken,
		RefreshToken:      tok.RefreshToken,
		RelayKeyExpiresAt: tok.ExpiresAt,
		OAuthClientID:     oauth2CLIClientID,
	}
	if err := config.Save(creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	dir, _ := config.ConfigDir()
	cliout.Printf(i18n.T("login.logged_in_saved"), style.Bold("EveryAPI"), dir)
	cliout.Println(i18n.T("login.next_hint"))
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
				if url == "" {
					// The verification_uri failed validation (untrusted /
					// malformed gateway response), so there is nothing safe
					// to copy — ignore the keystroke rather than claim a
					// bogus success or copy an empty clipboard.
					continue
				}
				// Raw mode means a bare "\n" stays in the same column.
				// Use "\r\n" so the message lines up at column zero.
				if cerr := cliprompt.CopyToClipboard(url); cerr == nil {
					fmt.Fprint(cliout.Out, "\r\n"+i18n.T("login.url_copied")+"\r\n")
				} else {
					fmt.Fprintf(cliout.Out, "\r\n"+i18n.T("login.url_copy_failed")+"\r\n", cerr)
				}
			case 0x03, 0x04: // ^C, ^D
				cancelPoll()
				return
			}
		}
	}()
	return stop
}
