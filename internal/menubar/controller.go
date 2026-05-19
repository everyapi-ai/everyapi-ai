package menubar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// State enumerates the controller's user-visible modes. The menu's
// item visibility is a pure function of State; no other layout
// branching happens elsewhere.
type State int

const (
	StateLoggedOut State = iota
	StateLoggingIn
	StateLoggedIn
)

// refreshIntervalDefault is the baseline cadence for the data
// refresh ticker. Preferences may override (down to a floor) so
// power users can chase faster top-up feedback or back off for
// rate-limit politeness. 30s balances liveness ("did my top-up
// land?") with API friendliness; the user can always click
// "Refresh now" to skip the wait.
const (
	refreshIntervalDefault = 30 * time.Second
	refreshIntervalFloor   = 10 * time.Second // prefs can't go below this
	// staleThreshold is how many consecutive refresh failures it
	// takes before the menu surfaces the "connection lost" banner.
	// 3 is the smallest value where one-off blips (lid wake, DNS
	// flutter) don't fire and a real outage trips it within ~90s
	// at the default cadence.
	staleThreshold = 3
)

// resolveRefreshInterval picks the active cadence given a prefs
// override. Out-of-range / zero values fall back to the default.
func (c *Controller) resolveRefreshInterval() time.Duration {
	if c.prefs.RefreshIntervalSeconds <= 0 {
		return refreshIntervalDefault
	}
	d := time.Duration(c.prefs.RefreshIntervalSeconds) * time.Second
	if d < refreshIntervalFloor {
		return refreshIntervalFloor
	}
	return d
}

// fallbackAPIBase returns the prefs-overridden default API base if
// the user supplied one, otherwise the hardcoded production gateway.
// Only consulted when the controller doesn't have credentials yet
// (sign-in path) or doesn't have an APIBase saved in creds. Once
// credentials.json is in place its APIBase wins.
func (c *Controller) fallbackAPIBase() string {
	if c.prefs.APIBase != "" {
		return c.prefs.APIBase
	}
	return config.DefaultAPIBase
}

// Controller is the long-running menubar state machine. One per
// process — instantiate in onReady, call Run on a goroutine.
type Controller struct {
	menu      menuView // production: *menuItems; tests substitute a recorder
	items     *menuItems // raw handles for click-channel wiring; nil in tests
	sanitizer sanitizerRunner

	mu          sync.Mutex
	state       State
	creds       *config.Credentials
	cancelLogin context.CancelFunc

	// shutdownCtx is the root for every goroutine spawned by the
	// controller (background refresh, risk watcher, in-flight
	// OAuth flows). handleQuit cancels it; long-running handlers
	// derive their own context.WithTimeout(s) from it so a Quit
	// click tears down outstanding HTTP work promptly rather than
	// leaving zombie goroutines until their per-call timeout fires.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc

	// claudeClient holds the cookie-jar'd HTTP client between the
	// Claude OAuth /start and the user's later paste-back click.
	// Backend stores flow state in a session keyed by cookie, so
	// /start and /complete MUST share the same client. nil means
	// no Claude flow is in progress.
	claudeClient *api.Client

	// channelStatusCache is the risk watcher's memory of each
	// seller channel's last-observed status. Populated on first
	// poll; a transition `enabled → auto-disabled` fires a
	// notification on the next poll (see risk_watcher.go).
	channelStatusCache map[int]int

	// lastChannels mirrors the most recent ListSellerChannels
	// result (capped to channelSubmenuSlots). Powers the submenu
	// click handlers and the menu refresh path.
	lastChannels []api.SellerChannel

	// prefs is loaded once at construction. Edits via the
	// "Preferences…" item land in menubar-prefs.json; the user is
	// instructed to restart for them to take effect. Live reload
	// is intentionally out of scope — these knobs are once-a-month
	// settings, not high-frequency adjustments.
	prefs preferences

	// sanitizerListen is the bind address handed to the in-process
	// sanitizer proxy when the user toggles it on. Resolved at
	// construction from prefs (falling back to
	// defaultSanitizerListen) — kept on the Controller rather than
	// a package var so tests can override without racing other
	// Controller instances.
	sanitizerListen string

	// refreshFailureStreak counts consecutive failed refreshes. The
	// staleWarn menu item lights up once we cross
	// staleThreshold so the user knows the displayed numbers may
	// not reflect server reality. Reset on every successful
	// refresh.
	refreshFailureStreak int

	// lastSellerQuota tracks the seller earnings number across
	// refresh cycles so we can notify on increase. -1 sentinel
	// means "never observed" — used to suppress the notification
	// on the very first poll after sign-in, otherwise the user
	// would get a spurious "earnings up" toast for whatever
	// number was already in the account.
	lastSellerQuota int

	// kick is fed by click handlers and the refresh ticker. The main
	// loop drains it and dispatches; channelling all mutation
	// through a single goroutine keeps state transitions race-free
	// without sprinkling locks across the codebase.
	kick chan command
}

type command int

const (
	cmdSignIn command = iota
	cmdRefreshNow
	cmdOpenWeb
	cmdSignOut
	cmdQuit
	cmdRefreshTick
	cmdLoginResult
	cmdSanitizerToggle
	cmdManageChannels // jump-phrase to dashboard /seller/channels
	cmdAddClaude      // in-menubar OAuth flow, paste-back via clipboard
	cmdAddGemini      // in-menubar OAuth flow, loopback callback
	cmdPasteClaude    // user clicked paste after copying from Anthropic
	cmdCopyRelayKey   // resolve + clipboard-write the relay API key
	cmdTopUp
	cmdAbout
	cmdOpenDocs
	cmdRevealConfig
	cmdReportIssue
	cmdPreferences
)

// loginOutcome is non-nil only when cmdLoginResult is enqueued; the
// controller stores it in a one-slot side channel to avoid plumbing a
// payload through every command. Read by the dispatch right after
// cmdLoginResult fires.
type loginOutcome struct {
	creds *config.Credentials
	err   error
}

// New constructs a Controller and builds the static menu. Must be
// called from systray.Run's onReady callback (it touches systray
// internals that require an active Run loop).
func New() *Controller {
	prefs := loadPrefs()
	listen := defaultSanitizerListen
	if prefs.SanitizerListen != "" {
		listen = prefs.SanitizerListen
	}
	// Route TLS pin mismatches into a desktop notification. The CLI
	// logs to stderr but a menubar user never sees that surface;
	// without this hook the report-only pin would be silently
	// ignored by every detached macOS / Windows / Linux session.
	api.PinMismatchHook = func(host, pin, msg string) {
		notify("EveryAPI — TLS public-key pin mismatch on "+host,
			"This usually means a corporate/ISP TLS proxy — but it can also indicate an attack. The connection was ALLOWED (pinning is report-only).")
	}
	items := buildMenu()
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	c := &Controller{
		menu:            items,
		items:           items,
		kick:            make(chan command, 8),
		lastSellerQuota: -1,
		prefs:           prefs,
		sanitizerListen: listen,
		shutdownCtx:     shutdownCtx,
		shutdownCancel:  shutdownCancel,
	}
	c.menu.applyLoggedOut() // overwritten by initial state below
	return c
}

// newForTest builds a Controller wired to a fake menu — used in
// _test.go files that exercise state transitions without spinning
// up a systray. Live click-channel wiring is skipped; tests drive
// the dispatch by enqueueing commands on c.kick directly.
func newForTest(menu menuView) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	return &Controller{
		menu:            menu,
		items:           nil,
		kick:            make(chan command, 8),
		lastSellerQuota: -1,
		sanitizerListen: defaultSanitizerListen,
		shutdownCtx:     ctx,
		shutdownCancel:  cancel,
	}
}

// Run drives the controller until the user picks Quit. Blocks until
// then; intended to run in a goroutine spawned from onReady.
func (c *Controller) Run() {
	// Wire menu-item click handlers. Each handler is a single-line
	// goroutine that forwards to the kick channel; menu-item
	// ClickedCh's are 1-buffered so this never blocks.
	if c.items != nil {
		go forwardClicks(c.items.signIn.ClickedCh, c.kick, cmdSignIn)
		go forwardClicks(c.items.refreshNow.ClickedCh, c.kick, cmdRefreshNow)
		go forwardClicks(c.items.openWeb.ClickedCh, c.kick, cmdOpenWeb)
		go forwardClicks(c.items.signOut.ClickedCh, c.kick, cmdSignOut)
		go forwardClicks(c.items.sanitizer.ClickedCh, c.kick, cmdSanitizerToggle)
		go forwardClicks(c.items.addClaude.ClickedCh, c.kick, cmdAddClaude)
		go forwardClicks(c.items.addGemini.ClickedCh, c.kick, cmdAddGemini)
		go forwardClicks(c.items.pasteClaude.ClickedCh, c.kick, cmdPasteClaude)
		go forwardClicks(c.items.copyRelayKey.ClickedCh, c.kick, cmdCopyRelayKey)
		go forwardClicks(c.items.manageChannels.ClickedCh, c.kick, cmdManageChannels)
		go forwardClicks(c.items.topup.ClickedCh, c.kick, cmdTopUp)
		go forwardClicks(c.items.about.ClickedCh, c.kick, cmdAbout)
		go forwardClicks(c.items.openDocs.ClickedCh, c.kick, cmdOpenDocs)
		go forwardClicks(c.items.revealConfig.ClickedCh, c.kick, cmdRevealConfig)
		go forwardClicks(c.items.reportIssue.ClickedCh, c.kick, cmdReportIssue)
		go forwardClicks(c.items.preferences.ClickedCh, c.kick, cmdPreferences)
		go forwardClicks(c.items.quit.ClickedCh, c.kick, cmdQuit)
		// Channel-submenu clicks open the per-channel dashboard
		// page. Index in c.items.channelChildren maps 1:1 to
		// c.lastChannels[i] populated by applyChannelRiskDelta.
		for i, child := range c.items.channelChildren {
			idx := i
			go func(ch <-chan struct{}) {
				for range ch {
					c.handleChannelClick(idx)
				}
			}(child.ClickedCh)
		}
	}

	// Background refresh ticker — fires every refreshInterval and
	// enqueues a refresh command. The dispatch ignores it unless
	// the controller is StateLoggedIn so we don't hammer the
	// unauthenticated API endpoints.
	tk := time.NewTicker(c.resolveRefreshInterval())
	defer tk.Stop()
	go func() {
		for range tk.C {
			select {
			case c.kick <- cmdRefreshTick:
			default: // drop if a refresh is already pending
			}
		}
	}()

	// Risk watcher runs its own slow-tick loop. It self-gates on
	// the logged-in state, so we don't need to wire start/stop to
	// auth transitions — fire it once at Run start, derive from
	// shutdownCtx so Quit tears it down with everything else.
	go c.startRiskWatcher(c.shutdownCtx)

	// Initial state from creds-on-disk. Done synchronously so the
	// first paint shows the right items before any user interaction.
	c.applyInitialState()

	// Main dispatch.
	loginOut := make(chan loginOutcome, 1)
	for cmd := range c.kick {
		switch cmd {
		case cmdSignIn:
			c.handleSignIn(loginOut)
		case cmdRefreshNow:
			// refresh is HTTP I/O (up to 15s) — must not block the
			// dispatch loop or clicks queue invisibly. Root at
			// shutdownCtx so Quit cancels in-flight refreshes.
			go c.refresh(c.shutdownCtx)
		case cmdOpenWeb:
			c.handleOpenWeb()
		case cmdSignOut:
			c.handleSignOut()
		case cmdQuit:
			c.handleQuit()
			return
		case cmdRefreshTick:
			if c.getState() == StateLoggedIn {
				go c.refresh(c.shutdownCtx)
			}
		case cmdLoginResult:
			// cmdSignIn guards on StateLoggedOut and we only enqueue
			// cmdLoginResult AFTER successfully sending loginOut, so
			// there's always exactly one value pending here. Read
			// blocking — the earlier defensive select-default branch
			// hid a programming error behind a silent no-op.
			c.applyLoginOutcome(<-loginOut)
		case cmdSanitizerToggle:
			c.handleSanitizerToggle()
		case cmdManageChannels:
			go c.openViaJumpPhrase("channels", "EveryAPI — manage channels")
		case cmdAddClaude:
			go c.handleAddClaude()
		case cmdAddGemini:
			go c.handleAddGemini()
		case cmdPasteClaude:
			go c.handlePasteClaude()
		case cmdCopyRelayKey:
			go c.handleCopyRelayKey()
		case cmdTopUp:
			go c.openViaJumpPhrase("topup", "EveryAPI — top up / withdraw")
		case cmdAbout:
			go c.handleAbout()
		case cmdOpenDocs:
			go c.handleOpenDocs()
		case cmdRevealConfig:
			go c.handleRevealConfig()
		case cmdReportIssue:
			go c.handleReportIssue()
		case cmdPreferences:
			go c.handlePreferences()
		}
	}
}

// forwardClicks pumps a MenuItem's ClickedCh into the central command
// channel. The non-blocking send drops duplicate clicks (rapid double
// click is a single logical action).
func forwardClicks(src <-chan struct{}, dst chan<- command, cmd command) {
	for range src {
		select {
		case dst <- cmd:
		default:
		}
	}
}

func (c *Controller) getState() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *Controller) setState(s State) {
	c.mu.Lock()
	c.state = s
	c.mu.Unlock()
}

// recomputeIconState picks the right icon variant for the current
// (auth-state, channel-list) tuple and pushes it to the menu. Called
// whenever something that could affect the icon changes: login,
// logout, the channel poll. Pure read on c.mu — quick to hold.
func (c *Controller) recomputeIconState() {
	c.mu.Lock()
	icon := IconStateLoggedOut
	if c.state == StateLoggedIn {
		icon = IconStateLoggedIn
		for _, ch := range c.lastChannels {
			if ch.Status == channelStatusAutoDisable {
				icon = IconStateAlert
				break
			}
		}
	}
	c.mu.Unlock()
	c.menu.applyIconState(icon)
}

func (c *Controller) applyInitialState() {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		c.setState(StateLoggedOut)
		c.menu.applyLoggedOut()
		c.recomputeIconState()
		return
	}
	if err != nil {
		log.Printf("menubar: load credentials: %v", err)
		c.setState(StateLoggedOut)
		c.menu.applyLoggedOut()
		c.recomputeIconState()
		return
	}
	c.mu.Lock()
	c.creds = creds
	c.state = StateLoggedIn
	c.mu.Unlock()
	c.menu.applyLoggedIn(creds.Username)
	c.recomputeIconState()

	// Restore persisted UI state. Sanitizer is the only resumable
	// surface today; we deliberately only restore it when the user
	// is signed in (mirrors the menu's "sanitizer item visible only
	// when logged in" rule, and sidesteps a confusing state where
	// the proxy is up but the menu thinks it's offline).
	if st, err := loadState(); err == nil && st.SanitizerEnabled {
		upstream := creds.APIBase
		if upstream == "" {
			upstream = c.fallbackAPIBase()
		}
		if serr := c.sanitizer.Start(st.SanitizerListen, upstream); serr != nil {
			log.Printf("menubar: restore sanitizer: %v", serr)
		} else {
			c.menu.applySanitizerState(true, c.sanitizer.Listen())
		}
	} else if err != nil {
		log.Printf("menubar: load state: %v", err)
	}

	// First refresh on startup — fire-and-forget; the menu shows
	// "—" placeholders until this completes.
	go c.refresh(c.shutdownCtx)
}

func (c *Controller) handleSignIn(loginOut chan<- loginOutcome) {
	if c.getState() != StateLoggedOut {
		return
	}
	c.setState(StateLoggingIn)
	// Root at shutdownCtx so a Quit click cancels the device-auth
	// poll uniformly with every other controller-scoped goroutine.
	// cancelLogin keeps the per-flow cancel so the user clicking
	// Sign-out mid-flow can abort just this one.
	ctx, cancel := context.WithCancel(c.shutdownCtx)
	c.mu.Lock()
	c.cancelLogin = cancel
	c.mu.Unlock()

	go func() {
		apiBase := c.fallbackAPIBase()
		creds, err := runDeviceAuth(ctx, apiBase, func(userCode string) {
			c.menu.applyLoggingIn(userCode)
		})
		// Blocking send. The StateLoggedOut guard above prevents a
		// second concurrent producer, so the buffered slot is always
		// free. A defensive non-blocking send would either drop the
		// outcome (deadlock the dispatch reading <-loginOut later)
		// or paper over a real invariant violation — neither is the
		// behaviour we want.
		loginOut <- loginOutcome{creds: creds, err: err}
		c.kick <- cmdLoginResult
	}()
}

func (c *Controller) applyLoginOutcome(out loginOutcome) {
	c.mu.Lock()
	c.cancelLogin = nil
	c.mu.Unlock()
	if out.err != nil {
		log.Printf("menubar: sign-in failed: %v", out.err)
		c.setState(StateLoggedOut)
		c.menu.applyLoggedOut()
		return
	}
	c.mu.Lock()
	c.creds = out.creds
	c.state = StateLoggedIn
	c.lastSellerQuota = -1 // suppress notification on first refresh
	c.mu.Unlock()
	c.menu.applyLoggedIn(out.creds.Username)
	c.recomputeIconState()
	go c.refresh(c.shutdownCtx)
}

func (c *Controller) refresh(ctx context.Context) {
	c.mu.Lock()
	creds := c.creds
	c.mu.Unlock()
	if creds == nil {
		return
	}

	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	status, err := client.GetStatus(ctx)
	if err != nil {
		log.Printf("menubar: get-status: %v", err)
		c.markRefreshFailure()
		return
	}
	self, err := client.GetSelf(ctx)
	if err != nil {
		// 401 on a stored token means the session expired or was
		// revoked server-side. refresh() runs on a background
		// goroutine so we route the state change through the kick
		// channel — direct mutation would race with the main loop.
		// Don't bump the failure streak for 401s — the imminent
		// sign-out will clear the menu state cleanly.
		if api.IsUnauthorized(err) {
			log.Printf("menubar: session expired — dropping to logged-out")
			select {
			case c.kick <- cmdSignOut:
			default:
			}
			return
		}
		log.Printf("menubar: get-self: %v", err)
		c.markRefreshFailure()
		return
	}
	c.markRefreshSuccess()

	perUnit := status.QuotaPerUnit
	if perUnit <= 0 {
		perUnit = 1
	}
	c.menu.applyData(
		formatUSD(self.Quota, perUnit),
		formatUSD(self.UsedQuota, perUnit),
		self.RequestCount,
		formatUSD(int64(self.SellerQuota), perUnit),
		self.SellerQuota > 0,
	)
	c.maybeNotifySellerEarnings(self.SellerQuota, perUnit)
}

// markRefreshFailure bumps the consecutive-failures counter and
// surfaces the stale banner exactly on the threshold-crossing tick
// (idempotent past that — subsequent failures within the same
// streak don't re-toggle the menu).
func (c *Controller) markRefreshFailure() {
	c.mu.Lock()
	prev := c.refreshFailureStreak
	c.refreshFailureStreak++
	justCrossed := prev < staleThreshold && c.refreshFailureStreak >= staleThreshold
	c.mu.Unlock()
	if justCrossed {
		c.menu.applyStale(true)
	}
}

// markRefreshSuccess clears the failure streak + the stale banner.
// Idempotent — calling on a healthy session is a no-op.
func (c *Controller) markRefreshSuccess() {
	c.mu.Lock()
	wasStale := c.refreshFailureStreak >= staleThreshold
	c.refreshFailureStreak = 0
	c.mu.Unlock()
	if wasStale {
		c.menu.applyStale(false)
	}
}

// maybeNotifySellerEarnings fires a desktop notification when the
// seller_quota field increases between two consecutive successful
// refreshes. The first observation after sign-in is recorded
// without notifying — we don't know whether the current number
// represents fresh earnings or a long-standing balance.
//
// V1.1 limit: no per-sale granularity (no event stream from the
// backend yet — see GOAL.md M4). A single notification covers any
// delta, including multiple sales coalesced into one poll.
func (c *Controller) maybeNotifySellerEarnings(currentQuota int, perUnit float64) {
	c.mu.Lock()
	prev := c.lastSellerQuota
	c.lastSellerQuota = currentQuota
	muted := c.prefs.MuteEarnings
	c.mu.Unlock()
	if prev < 0 || currentQuota <= prev || muted {
		return
	}
	deltaUSD := formatUSD(int64(currentQuota-prev), perUnit)
	totalUSD := formatUSD(int64(currentQuota), perUnit)
	notify(
		"EveryAPI — seller earnings up "+deltaUSD,
		"Pending balance: "+totalUSD+". Open EveryAPI to withdraw.",
	)
}

func (c *Controller) handleOpenWeb() {
	c.mu.Lock()
	creds := c.creds
	c.mu.Unlock()
	apiBase := c.fallbackAPIBase()
	if creds != nil && creds.APIBase != "" {
		apiBase = creds.APIBase
	}
	if err := openBrowser(api.WebOriginFromBase(apiBase)); err != nil {
		log.Printf("menubar: open browser: %v", err)
	}
}

func (c *Controller) handleSignOut() {
	c.mu.Lock()
	c.creds = nil
	c.state = StateLoggedOut
	c.lastSellerQuota = -1
	c.claudeClient = nil // any in-flight Claude flow is abandoned
	c.channelStatusCache = nil
	c.lastChannels = nil
	c.refreshFailureStreak = 0
	c.mu.Unlock()
	if err := config.Delete(); err != nil {
		log.Printf("menubar: delete credentials: %v", err)
	}
	c.menu.applyClaudePastePending(false)
	c.menu.applyChannels(nil)
	c.menu.applyStale(false)
	c.menu.applyLoggedOut()
	c.recomputeIconState()
}

// handleAddClaude kicks off the Claude OAuth flow's first half:
// collect name + models, hit /start, open browser, surface the
// "Paste Claude auth from clipboard" item. Stashes the cookie-jar'd
// client on the controller so the later cmdPasteClaude click can
// finish in the same session.
//
// Concurrency: if a Claude flow is already in progress, this is a
// no-op (the existing one must be cancelled or completed first).
// The Gemini flow runs in parallel without coordination — different
// menu items, different backend session.
func (c *Controller) handleAddClaude() {
	c.mu.Lock()
	pending := c.claudeClient != nil
	c.mu.Unlock()
	if pending {
		notify("EveryAPI — Claude OAuth already in progress",
			"Paste the code from the previous flow or quit and retry.")
		return
	}

	ctx, cancel := context.WithTimeout(c.shutdownCtx, sellerOAuthTimeout)
	defer cancel()
	cli, ok, err := c.startClaudeOAuth(ctx)
	if err != nil {
		log.Printf("menubar: claude oauth start: %v", err)
		notify("EveryAPI — Claude OAuth failed to start", err.Error())
		return
	}
	if !ok {
		return // user canceled
	}
	c.mu.Lock()
	c.claudeClient = cli
	c.mu.Unlock()
	c.menu.applyClaudePastePending(true)
}

// handleAddGemini runs the full Gemini loopback flow inline. Unlike
// Claude this doesn't need menu state because the listener blocks
// inside the goroutine until Google redirects.
func (c *Controller) handleAddGemini() {
	ctx, cancel := context.WithTimeout(c.shutdownCtx, sellerOAuthTimeout+30*time.Second)
	defer cancel()
	if err := c.runGeminiOAuth(ctx); err != nil {
		log.Printf("menubar: gemini oauth: %v", err)
		notify("EveryAPI — Gemini OAuth failed", err.Error())
	}
}

// handlePasteClaude reads the clipboard and finishes the in-flight
// Claude flow. If no flow is pending the click is ignored (defensive:
// the menu item is supposed to be hidden in that state, but a stale
// click from a fast user could race).
func (c *Controller) handlePasteClaude() {
	c.mu.Lock()
	cli := c.claudeClient
	c.mu.Unlock()
	if cli == nil {
		return
	}
	pasted, err := readClipboard()
	if err != nil {
		log.Printf("menubar: clipboard read: %v", err)
		notify("EveryAPI — couldn't read clipboard", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.shutdownCtx, 30*time.Second)
	defer cancel()
	res, err := completeClaudeOAuth(ctx, cli, pasted)
	if err != nil {
		log.Printf("menubar: claude complete: %v", err)
		// Truncate err.Error() because backend errors can echo the
		// clipboard payload — without a cap, an adversarial paste
		// would render in the notification banner (screen-share /
		// recording leak surface).
		notify("EveryAPI — Claude OAuth failed", truncateForNotify(err.Error()))
		return
	}
	c.mu.Lock()
	c.claudeClient = nil
	c.mu.Unlock()
	c.menu.applyClaudePastePending(false)
	notify(
		fmt.Sprintf("EveryAPI — Claude channel #%d mounted", res.ChannelID),
		"Token expires: "+res.ExpiresAt+" (auto-refreshes before then).",
	)
}

// handleCopyRelayKey resolves the user's relay API key (sk-everyapi-…),
// writes it to the OS clipboard, and fires a "copied — prefix XYZ"
// notification. Failures (no key, transport, clipboard) surface as
// notifications too so the user always sees the result.
//
// The notification deliberately shows ONLY the first 16 chars of
// the key as a sanity-check signal — the full key is on the
// clipboard, but the notification banner is a screen-share /
// recording risk surface.
func (c *Controller) handleCopyRelayKey() {
	c.mu.Lock()
	creds := c.creds
	c.mu.Unlock()
	if creds == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.shutdownCtx, 15*time.Second)
	defer cancel()
	key, err := resolveRelayKey(ctx, creds)
	if err != nil {
		if errors.Is(err, errNoRelayKey) {
			notify("EveryAPI — no relay API key",
				"Create one in the dashboard, then try again.")
			return
		}
		log.Printf("menubar: resolve relay key: %v", err)
		notify("EveryAPI — couldn't resolve relay key", err.Error())
		return
	}
	if err := writeClipboard(key); err != nil {
		log.Printf("menubar: write clipboard: %v", err)
		notify("EveryAPI — couldn't write clipboard", err.Error())
		return
	}
	// Cache may have been mutated by resolveRelayKey — sync our
	// in-memory creds so the next click is fast-path.
	c.mu.Lock()
	if c.creds != nil {
		c.creds.RelayKey = key
	}
	c.mu.Unlock()
	notify("EveryAPI — relay key copied", "Prefix: "+relayKeyPrefix(key))
}

// handleChannelClick opens the dashboard's per-channel page in the
// browser. idx is the position in c.lastChannels; out-of-range
// (stale click after the list shrank) silently no-ops.
func (c *Controller) handleChannelClick(idx int) {
	c.mu.Lock()
	creds := c.creds
	var ch api.SellerChannel
	inRange := idx >= 0 && idx < len(c.lastChannels)
	if inRange {
		ch = c.lastChannels[idx]
	}
	c.mu.Unlock()
	if !inRange || creds == nil {
		return
	}
	apiBase := creds.APIBase
	if apiBase == "" {
		apiBase = c.fallbackAPIBase()
	}
	url := api.WebOriginFromBase(apiBase) + "/seller/channels/" + fmt.Sprintf("%d", ch.ID)
	if err := openBrowser(url); err != nil {
		log.Printf("menubar: open channel %d: %v", ch.ID, err)
	}
}

// handleSanitizerToggle flips the in-process proxy. Bind uses the
// CLI default (127.0.0.1:8888); a future config item can override.
// On error we leave the checkmark as-is — the menu refreshes via
// applySanitizerState below.
//
// The new state is persisted synchronously so a crash mid-toggle
// doesn't leave the menubar booting in the inverted mode. Save
// failures are logged but don't undo the in-memory toggle — UX
// would be worse if the user clicked, saw nothing happen, and
// then had to debug a disk-write issue.
func (c *Controller) handleSanitizerToggle() {
	if c.sanitizer.Running() {
		c.sanitizer.Stop()
	} else {
		c.mu.Lock()
		creds := c.creds
		c.mu.Unlock()
		upstream := c.fallbackAPIBase()
		if creds != nil && creds.APIBase != "" {
			upstream = creds.APIBase
		}
		if err := c.sanitizer.Start(c.sanitizerListen, upstream); err != nil {
			log.Printf("menubar: sanitizer start: %v", err)
		}
	}
	running := c.sanitizer.Running()
	c.menu.applySanitizerState(running, c.sanitizer.Listen())
	if err := saveState(persistedState{
		SanitizerEnabled: running,
		SanitizerListen:  c.sanitizer.Listen(),
	}); err != nil {
		log.Printf("menubar: persist state: %v", err)
	}
}

// systrayQuit indirects through a package var so tests can drive
// Run() without spawning a real systray. The real implementation is
// systray.Quit which signals systray.Run to return on the main
// goroutine.
var systrayQuit = systray.Quit

func (c *Controller) handleQuit() {
	c.mu.Lock()
	if c.cancelLogin != nil {
		c.cancelLogin()
	}
	c.mu.Unlock()
	// Cancel every controller-scoped goroutine (refresh, risk
	// watcher, in-flight Claude/Gemini OAuth) so they tear down
	// promptly instead of waiting for their per-call timeouts.
	if c.shutdownCancel != nil {
		c.shutdownCancel()
	}
	c.sanitizer.Stop()
	systrayQuit()
}
