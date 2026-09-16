package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Login runs the device authorization flow: POST start → show user the QR code + code + URL → user scans on phone OR opens browser → poll until authorized → write credentials.
//
// The QR encodes the verification URL with `?code=` pre-filled, so a phone scan lands on the confirm page with the code already in the input — no retyping the 8-character string on a tiny keyboard. docs/cli/channel-marketplace.md §7-5 Layer 1 (device-to-device QR sign-in) realised here, on top of the existing device-auth backend that #133 shipped.
//
// Flags:
//
//	--api-base <url>  override configured/default gateway (dev / self-host) --no-browser      skip the auto-open; user copies the URL manually --no-qr           skip the terminal QR (for non-UTF-8 terminals / pipes) --format          human (default) or the desktop-only json-lines protocol
func Login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	if loginMachineRequested(args) {
		fs.SetOutput(io.Discard)
	}
	apiBase := fs.String("api-base", "", "EveryAPI API base URL")
	noBrowser := fs.Bool("no-browser", false, "skip opening the browser automatically")
	noQR := fs.Bool("no-qr", false, "skip rendering the QR code (useful for non-UTF-8 terminals or when piping output)")
	format := fs.String("format", "human", "output format (human or json-lines)")
	alias := fs.String("alias", "", "name to store this account under (default: derived from the username)")
	if err := fs.Parse(args); err != nil {
		if loginMachineRequested(args) {
			return machineLoginError("invalid_request", err)
		}
		return err
	}
	if *format == "json-lines" {
		var usedHumanFlag bool
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "no-browser" || f.Name == "no-qr" || f.Name == "alias" {
				usedHumanFlag = true
			}
		})
		if usedHumanFlag || fs.NArg() != 0 {
			return machineLoginError("invalid_request", errors.New("machine login accepts only --api-base and --format=json-lines"))
		}
		return loginMachine(*apiBase)
	}
	if *format != "human" {
		return machineLoginError("invalid_request", fmt.Errorf("unsupported format %q", *format))
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
	// signalCtx (not withCtx): the device-auth poll below blocks for minutes. The "(Ctrl+C to cancel)" line we print must be true — cancel the in-flight poll on SIGINT instead of hard-killing.
	ctx, stop := cliout.SignalCtx()
	defer stop()

	start, oauth2, err := startDeviceFlow(ctx, client)
	if err != nil {
		return fmt.Errorf("start device authorization: %w", err)
	}

	// URL with code pre-filled so a phone QR scan lands on the dashboard confirm page with the input already populated. The fallback "type the code by hand" path still works against the bare verification_uri printed below.
	prefilledURL := buildVerificationURLWithCode(start.VerificationURI, start.UserCode)
	// The verification_uri is server-controlled. Only feed a well-formed http(s) URL to the QR renderer, clipboard, and browser launcher — a value carrying control bytes or a leading '-' would drive the terminal (OSC/CSI injection) or inject options into open/xdg-open. The printed URL text always goes through Sanitize regardless.
	safeURL := ""
	if isDisplayableURL(prefilledURL) {
		safeURL = prefilledURL
	}

	cliout.Println("")
	if !*noQR && safeURL != "" {
		cliout.Println(i18n.T("login.qr_hint"))
		cliout.Println("")
		// qrterminal renders to stdout with Unicode half-blocks by default (▀▄ etc.) — about half the height of the ASCII "▓▓" form. Level L recovery is fine for short URLs and keeps the QR small enough to fit a normal terminal.
		qrterminal.GenerateHalfBlock(safeURL, qrterminal.L, cliout.Out)
		cliout.Println("")
		cliout.Println(i18n.T("login.url_hint_with_qr"))
	} else {
		cliout.Println(i18n.T("login.url_hint"))
	}
	cliout.Printf("\n    %s\n\n", cliout.Sanitize(prefilledURL))
	// Surface the bare user_code too in case the dashboard fails to pre-fill (older /cli/auth deploys, query-stripping middlebox, user pasted the URL into a tool that drops query strings).
	cliout.Printf(i18n.T("login.code_hint")+"\n\n", style.Bold(cliout.Sanitize(start.UserCode)))

	if !*noBrowser && safeURL != "" {
		if err := cliprompt.OpenBrowser(safeURL); err == nil {
			cliout.Println(i18n.T("login.browser_opened"))
		} else {
			// stderr so a user piping `everyapi auth login | …` gets a clean stdout (the URL + code go through the cmd.Out writer above). xdg-open missing on a headless Linux desktop is the common case here.
			fmt.Fprintln(os.Stderr, i18n.T("common.browser_open_failed_qr"))
		}
	}

	// Probe stdin once so we pick the right hint copy AND only bother starting the raw-mode watcher when keystrokes are reachable.
	fd := int(os.Stdin.Fd())
	ttyIn := term.IsTerminal(fd)

	cliout.Println("")
	// Only advertise the 'c'-to-copy hint when there's a validated URL to copy. If the verification_uri failed validation (safeURL==""), the key watcher ignores 'c', so promising the copy would be a dead control — show the plain waiting line instead.
	if ttyIn && safeURL != "" {
		cliout.Println(i18n.T("login.waiting_with_copy"))
	} else {
		cliout.Println(i18n.T("login.waiting"))
	}

	// Wrap ctx in WithCancel so the raw-mode reader (which swallows SIGINT — see startLoginKeyWatcher) can still propagate Ctrl+C as a context cancellation. Outside raw mode the existing SignalCtx already cancels ctx on SIGINT, so this is a no-op passthrough then.
	pctx, cancelPoll := context.WithCancel(ctx)
	defer cancelPoll()

	stopWatcher := func() {}
	if ttyIn {
		// Pass safeURL: the 'c' keystroke copies it to the clipboard, so an untrusted/malformed verification_uri must not be copyable.
		stopWatcher = startLoginKeyWatcher(fd, safeURL, cancelPoll)
	}
	defer stopWatcher()

	// OAuth2 fallback path: the access token is itself the relay key, so it's saved directly with no management session / relay-key resolution.
	if oauth2 {
		return finishOAuth2Login(pctx, resolvedAPIBase, *alias, client, start, stopWatcher)
	}

	res, err := client.PollUntilDone(pctx, start.DeviceCode, start.Interval)
	// Restore terminal BEFORE further printing — the success / error branches below use plain "\n", which renders as a column-zero newline only in cooked mode. stopWatcher is idempotent so the deferred call is harmless.
	stopWatcher()
	if err != nil {
		switch err {
		case api.ErrDeviceAuthExpired:
			return fmt.Errorf("the code timed out before you authorized — run 'everyapi auth login' again")
		case api.ErrDeviceAuthDenied:
			return fmt.Errorf("authorization was denied in the browser")
		default:
			if errors.Is(err, context.Canceled) {
				// Ctrl+C during the poll cancels the context; the UI told the user "Ctrl+C to cancel", so exit cleanly instead of dumping "Error: poll: context canceled" with a non-zero status.
				cliout.Println(i18n.T("login.cancelled"))
				return nil
			}
			return fmt.Errorf("poll: %w", err)
		}
	}

	creds, account, err := saveLegacyLoginCredentials(ctx, resolvedAPIBase, *alias, res)
	if err != nil {
		return err
	}

	cliout.Printf(i18n.T("login.logged_in_saved"), style.Bold(cliout.Sanitize(res.Username)), loginCredentialPath(account))
	printLoginAccountNotice(account)

	// Resolve the relay API key now (and cache it) so `everyapi use` works on first try. The access token alone can't relay — it's a management credential — so without this step `use` would 401.
	//
	// A failure here is non-fatal: login itself already succeeded and credentials are saved. We only surface the errNoRelayKey sentinel (account has zero enabled keys — an actionable state the user must fix in the dashboard). Other failures (transient 5xx, network blip) are SWALLOWED: after a successful device-auth flow, a noisy "warning: ..." line on stderr would make the user doubt the login itself, and the next `everyapi use` / `everyapi status` will retry the resolution anyway.
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

// oauth2CLIClientID is the CLI's first-party OAuth2 client id (seeded in the backend). Used only on the fallback path.
const oauth2CLIClientID = "everyapi-cli"

// startDeviceFlow begins device authorization, preferring the legacy /api/cli/device-auth-* flow — which yields a management session the CLI's status/token commands rely on — and falling back to the OAuth2 device grant only when the legacy endpoint is absent (404). The bool reports whether the OAuth2 flow was used.
type deviceFlowStarter interface {
	DeviceAuthStart(context.Context) (*api.DeviceAuthStartResp, error)
	OAuth2DeviceStart(context.Context, string) (*api.DeviceAuthStartResp, error)
}

func startDeviceFlow(ctx context.Context, client deviceFlowStarter) (*api.DeviceAuthStartResp, bool, error) {
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

// finishOAuth2Login completes the OAuth2 device flow. The issued access token is itself a relay key (sk-everyapi-…), so it's stored as both the relay key and the access token — the CLI is "logged in" and `everyapi use` relays. There is no management session in this mode, so role lookup, status, and token-admin commands are limited.
func finishOAuth2Login(ctx context.Context, apiBase, alias string, client *api.Client, start *api.DeviceAuthStartResp, stopWatcher func()) error {
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
				// Ctrl+C during the poll cancels the context; the UI told the user "Ctrl+C to cancel", so exit cleanly instead of dumping "Error: poll: context canceled" with a non-zero status.
				cliout.Println(i18n.T("login.cancelled"))
				return nil
			}
			return fmt.Errorf("poll: %w", err)
		}
	}
	// The access token is itself the relay key; keep the refresh token + expiry so ResolveRelayKey can renew it before the 90-day key lapses.
	_, account, err := saveOAuth2LoginCredentials(apiBase, alias, tok)
	if err != nil {
		return err
	}
	cliout.Printf(i18n.T("login.logged_in_saved"), style.Bold("EveryAPI"), loginCredentialPath(account))
	printLoginAccountNotice(account)
	cliout.Println(i18n.T("login.next_hint"))
	return nil
}

// loginCredentialPath is the file the completed login actually wrote, for the "credentials saved to …" line. It is resolved from the account the login landed on rather than assumed to be credentials.json, because `everyapi --account <name> auth login` renews a parked account and writes accounts/<name>.json — naming credentials.json there would point the user at a file this login did not touch and imply the active account changed when it did not.
//
// Falls back to the config directory if the path cannot be resolved: the message is orientation, and a login that succeeded must not fail on it.
func loginCredentialPath(account loginAccountResult) string {
	if account.Name != "" {
		if path, err := config.AccountPath(account.Name); err == nil {
			return path
		}
	}
	dir, _ := config.ConfigDir()
	return dir
}

// printLoginAccountNotice tells the user which account name the session landed under, and names the previously-active account when one was kept. Silent on a single-account machine that stayed single-account: introducing account names to someone who only has one would be noise.
func printLoginAccountNotice(account loginAccountResult) {
	if account.Parked == "" {
		return
	}
	cliout.Printf(i18n.T("login.account_parked")+"\n",
		cliout.Sanitize(account.Parked),
		style.Bold(cliout.Sanitize(account.Name)))
	cliout.Println(i18n.T("login.account_switch_hint"))
}

// saveLegacyLoginCredentials owns the flow-independent completion work shared by interactive and desktop machine login: enrich the account metadata when possible and atomically persist the management credential.
func saveLegacyLoginCredentials(ctx context.Context, apiBase, alias string, res *api.DeviceAuthPollResult) (*config.Credentials, loginAccountResult, error) {
	creds := &config.Credentials{
		APIBase:     apiBase,
		AccessToken: res.AccessToken,
		UserID:      res.UserID,
		Username:    res.Username,
	}
	// Role lookup is an optional enrichment. A slow/unavailable management endpoint must not turn an otherwise successful device authorization into a failed login.
	roleCtx, roleCancel := context.WithTimeout(ctx, 10*time.Second)
	if self, err := api.New(apiBase, res.AccessToken).
		WithUserID(res.UserID).
		GetSelf(roleCtx); err == nil {
		creds.Role = self.Role
		creds.AvatarURL = self.AvatarURL
	}
	roleCancel()
	outcome, err := commitLoginCredentials(creds, alias)
	if err != nil {
		return nil, loginAccountResult{}, err
	}
	return creds, outcome, nil
}

// saveOAuth2LoginCredentials persists the OAuth fallback bundle for both human and machine login. The access token is also the relay key in this flow.
func saveOAuth2LoginCredentials(apiBase, alias string, tok *api.OAuth2Token) (*config.Credentials, loginAccountResult, error) {
	creds := &config.Credentials{
		APIBase:           apiBase,
		AccessToken:       tok.AccessToken,
		RelayKey:          tok.AccessToken,
		RefreshToken:      tok.RefreshToken,
		RelayKeyExpiresAt: tok.ExpiresAt,
		OAuthClientID:     oauth2CLIClientID,
	}
	outcome, err := commitLoginCredentials(creds, alias)
	if err != nil {
		return nil, loginAccountResult{}, err
	}
	return creds, outcome, nil
}

// loginAccountResult reports where a completed login landed: the account name it is stored under, and the name of the previously-active account that was parked to make room (empty when nothing was parked).
type loginAccountResult struct {
	Name   string
	Parked string
}

// commitLoginCredentials persists a completed login into the multi-account store.
//
// Signing into a DIFFERENT account keeps the previous one: it is parked under its own name in accounts/ and the new credential becomes active. Signing into the SAME account again is an ordinary refresh and overwrites in place — re-login after an expiry must not mint a second copy of one account, and that holds whether the account being re-authenticated is the active one or a parked one (see parkedAccountFor).
//
// When the process was pinned with `--account <name>`, the login refreshes THAT slot and leaves the active account alone, which is how a parked account whose session expired gets renewed without switching the machine over to it and back.
func commitLoginCredentials(creds *config.Credentials, alias string) (loginAccountResult, error) {
	alias = strings.TrimSpace(alias)
	if alias != "" && !config.ValidAccountName(alias) {
		return loginAccountResult{}, fmt.Errorf(i18n.T("accounts.invalid_name"), alias, config.MaxAccountNameLen)
	}
	if pinned := config.SelectedAccountName(); pinned != "" {
		if alias != "" && alias != pinned {
			return loginAccountResult{}, errors.New(i18n.T("login.alias_with_account"))
		}
		// `--account <name> auth login` means "renew that slot", so the credential that comes back has to belong to that slot. Authenticating as somebody else would replace the stored credential while the name kept saying otherwise — the pinned account's session gone, with the listing still showing its name. A credential that cannot be read is not a contradiction, so that case refreshes as before; the plain `auth login` below is the path that adds a different account under a name of its own.
		if stored, storedErr := config.LoadAccount(pinned); storedErr == nil && !sameLoginAccount(stored, creds) {
			return loginAccountResult{}, fmt.Errorf(i18n.T("login.account_mismatch"), pinned)
		}
		if err := config.Save(creds); err != nil {
			return loginAccountResult{}, fmt.Errorf("save credentials: %w", err)
		}
		return loginAccountResult{Name: pinned}, nil
	}

	// A corrupt or unreadable active credential is treated as "no account there": it is about to be replaced, and refusing the login would leave the user with no way to repair it from the CLI.
	activeCreds, loadErr := config.Load()
	if loadErr != nil {
		activeCreds = nil
	}
	activeName, err := config.ActiveAccountName()
	if err != nil {
		return loginAccountResult{}, err
	}

	refreshesActive := activeCreds != nil && sameLoginAccount(activeCreds, creds)
	name := alias
	// Set when the login re-authenticates an account that is currently PARKED: that slot is refreshed rather than duplicated, and its superseded credential file has to go before the new one takes the name.
	staleParked := ""
	switch {
	case refreshesActive:
		if name == "" {
			name = activeName
		}
	case alias == "":
		// Both computed before anything moves, so the active account is still visible and the new name cannot collide with it.
		if existing := parkedAccountFor(creds); existing != "" {
			name, staleParked = existing, existing
			break
		}
		name, err = config.UniqueAccountName(config.DeriveAccountName(creds))
		if err != nil {
			return loginAccountResult{}, err
		}
	}
	// An explicit alias is a request for that exact name, so a collision is an error rather than something to silently disambiguate into alias-2. The check covers a re-login into the account already active too — `--alias work` while signed in as `home` would otherwise just point the index at `work`, hiding the parked account that already holds the name behind a credential that is not its own and destroying it on the next switch. Only the no-op case is exempt: renaming the active account to the name it already has.
	if alias != "" && !(refreshesActive && alias == activeName) {
		taken, existsErr := config.AccountExists(alias)
		if existsErr != nil {
			return loginAccountResult{}, existsErr
		}
		if taken {
			return loginAccountResult{}, fmt.Errorf(i18n.T("accounts.exists"), alias)
		}
	}

	// Dropped before anything else moves. DeleteAccount resolves a name through whichever account is active RIGHT NOW, so this is the only point at which it is guaranteed to reach accounts/<name>.json instead of the credentials.json that is about to carry the same name. What it removes is one login older than the credential being written a few lines down.
	if staleParked != "" {
		if err := config.DeleteAccount(staleParked); err != nil {
			return loginAccountResult{}, err
		}
	}
	parked := ""
	if !refreshesActive && activeCreds != nil {
		if err := config.ParkActive(activeName); err != nil {
			return loginAccountResult{}, err
		}
		parked = activeName
	}
	if err := config.Save(creds); err != nil {
		return loginAccountResult{}, fmt.Errorf("save credentials: %w", err)
	}
	if err := config.SetActiveAccountName(name); err != nil {
		return loginAccountResult{}, err
	}
	return loginAccountResult{Name: name, Parked: parked}, nil
}

// parkedAccountFor returns the name of the stored, non-active account whose credential identifies the same account as creds, or "" when none does.
//
// Re-authenticating a PARKED account is the ordinary fix for a parked session that expired, and without this it lands as a brand-new account: `work` keeps the superseded token while the live session shows up as `work-2`. That is a second row for one account in `auth accounts list`, and — because a new login does not revoke the old token — a still-usable credential left on disk under the name the user recognizes.
//
// Best effort: a listing or a credential this cannot read simply means no match, and the login proceeds as a new account rather than failing.
func parkedAccountFor(creds *config.Credentials) string {
	infos, err := config.ListAccounts()
	if err != nil {
		return ""
	}
	for _, info := range infos {
		if info.Active {
			continue
		}
		stored, loadErr := config.LoadAccount(info.Name)
		if loadErr != nil {
			continue
		}
		if sameLoginAccount(stored, creds) {
			return info.Name
		}
	}
	return ""
}

// sameLoginAccount reports whether two credentials identify one account, i.e. whether a login should overwrite in place rather than park the old one. The rule lives on Credentials because the store applies it too — the accounts index stamps the active credential's identity and checks it back — and two copies of "what makes an account the same account" would be one copy too many.
func sameLoginAccount(a, b *config.Credentials) bool { return a.SameAccount(b) }

// startLoginKeyWatcher puts stdin into raw mode for the duration of the device-auth poll and watches for single keystrokes:
//
//	c / C   — copy the prefilled verification URL to the clipboard ^C / ^D — cancel the poll (raw mode swallows SIGINT, so we have to propagate cancellation through ctx ourselves)
//
// Returns an idempotent stop func that restores the terminal. Caller must invoke it before any subsequent printing — in raw mode "\n" alone leaves the cursor mid-line and the success message would look like staircase output otherwise.
//
// Robustness notes: when MakeRaw fails (rare — non-tty fd, locked- down container) we return a no-op so the rest of login still works; the URL is already on screen for manual copy. The reader goroutine outlives stop() in the edge case where it's still blocked on os.Stdin.Read — acceptable because login is a short-lived CLI command and the OS reaps the goroutine on process exit; the `stopped` flag stops it from acting on any keystroke that sneaks in between Restore and process exit.
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
					// The verification_uri failed validation (untrusted / malformed gateway response), so there is nothing safe to copy — ignore the keystroke rather than claim a bogus success or copy an empty clipboard.
					continue
				}
				// Raw mode means a bare "\n" stays in the same column. Use "\r\n" so the message lines up at column zero.
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
