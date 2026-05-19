package menubar

import "fyne.io/systray"

// menuView is the subset of menu mutations the controller invokes.
// Promoted to an interface so unit tests can swap in a fake recorder
// — fyne.io/systray's real MenuItem requires a live systray.Run loop
// that can't run inside `go test`. The production implementation is
// menuItems below.
type menuView interface {
	applyLoggedOut()
	applyLoggingIn(userCode string)
	applyLoggedIn(username string)
	applyData(quotaUSD, usedUSD string, requests int64, sellerUSD string, hasSeller bool)
	applySanitizerState(running bool, listen string)
	applyClaudePastePending(pending bool)
	applyChannels(channels []channelMenuRow)
	applyIconState(state IconState)
	applyStale(stale bool)
}

// channelMenuRow is the menubar-friendly slice of an
// api.SellerChannel: just the bits the submenu needs to display +
// an ID for the click target. Decoupled from the api type so tests
// don't have to import api.SellerChannel.
type channelMenuRow struct {
	ID     int
	Title  string // pre-formatted display string, e.g. "claude-prod ✓"
	Status int    // for tooltip + click handler dispatch
}

// menuItems holds every systray.MenuItem the controller mutates. The
// fyne.io/systray API only supports building the menu once and
// mutating items afterwards (no wholesale reassign — see fyne#2860),
// so we allocate every possible item up front and toggle visibility /
// enabled state per logical state.
//
// Layout (top to bottom):
//
//	────── logged-in only ──────
//	username                       (disabled, informational)
//	Quota: $X.XX remaining         (disabled)
//	Used:  $Y.YY                   (disabled)
//	Requests: N                    (disabled)
//	Seller earnings: $Z.ZZ         (disabled, hidden if zero)
//	──── always ────
//	[separator]
//	Sign in…                        (hidden when logged-in)
//	Code: ABCD-1234                 (hidden unless logging-in)
//	Refresh now
//	Open dashboard…
//	Sign out                        (hidden when logged-out)
//	[separator]
//	Quit EveryAPI
type menuItems struct {
	staleWarn      *systray.MenuItem
	username       *systray.MenuItem
	quota          *systray.MenuItem
	used           *systray.MenuItem
	requests       *systray.MenuItem
	seller         *systray.MenuItem
	signIn         *systray.MenuItem
	loginCode      *systray.MenuItem
	copyRelayKey   *systray.MenuItem
	sanitizer      *systray.MenuItem
	addClaude      *systray.MenuItem
	addGemini      *systray.MenuItem
	pasteClaude    *systray.MenuItem
	manageChannels *systray.MenuItem
	channelsParent *systray.MenuItem
	channelChildren []*systray.MenuItem // first N channels as submenu items
	topup           *systray.MenuItem
	refreshNow      *systray.MenuItem
	openWeb         *systray.MenuItem
	signOut         *systray.MenuItem
	helpParent      *systray.MenuItem
	about           *systray.MenuItem
	openDocs        *systray.MenuItem
	revealConfig    *systray.MenuItem
	reportIssue     *systray.MenuItem
	preferences     *systray.MenuItem
	quit            *systray.MenuItem
}

// channelSubmenuSlots caps the number of channel submenu items we
// pre-allocate. Five mirrors the dashboard's "recent" lists and is
// enough for the common case (most sellers run 1-3 channels); the
// jump-phrase "Manage channels (dashboard)" item handles overflow.
const channelSubmenuSlots = 5

// buildMenu constructs the static layout. It must be called from
// systray's onReady (i.e. before systray.Run returns control).
// MenuItem handles are returned for the controller to mutate.
func buildMenu() *menuItems {
	m := &menuItems{}

	// Surfaced only after 3+ consecutive refresh failures (see
	// Controller.refresh). Keeps the data items below visible
	// (last-known values are better than blank) but flags that
	// they may be stale.
	m.staleWarn = systray.AddMenuItem("⚠ Connection lost — data may be stale", "Couldn't reach EveryAPI on recent refresh attempts; the numbers below are the last successful read")
	m.staleWarn.Disable()
	m.staleWarn.Hide()

	m.username = systray.AddMenuItem("Not signed in", "")
	m.username.Disable()
	m.quota = systray.AddMenuItem("Quota: —", "Remaining account quota")
	m.quota.Disable()
	m.used = systray.AddMenuItem("Used: —", "Quota consumed this period")
	m.used.Disable()
	m.requests = systray.AddMenuItem("Requests: —", "Lifetime request count")
	m.requests.Disable()
	m.seller = systray.AddMenuItem("Seller earnings: —", "Pending marketplace earnings")
	m.seller.Disable()
	m.seller.Hide()

	systray.AddSeparator()

	m.signIn = systray.AddMenuItem("Sign in…", "Authenticate this device with EveryAPI")
	m.loginCode = systray.AddMenuItem("", "Code shown on the dashboard confirm page")
	m.loginCode.Disable()
	m.loginCode.Hide()

	systray.AddSeparator()

	m.copyRelayKey = systray.AddMenuItem("Copy relay API key", "Copy the relay API key (sk-everyapi-…) to the clipboard")
	m.copyRelayKey.Hide()
	m.sanitizer = systray.AddMenuItem("Sanitizer proxy: off", "Toggle the local privacy-sanitizing proxy")
	m.sanitizer.Hide() // surfaced once signed in
	m.addClaude = systray.AddMenuItem("Add Claude channel…", "Mount an Anthropic Claude subscription as a seller channel")
	m.addClaude.Hide()
	m.addGemini = systray.AddMenuItem("Add Gemini channel…", "Mount a Google Gemini account as a seller channel")
	m.addGemini.Hide()
	m.pasteClaude = systray.AddMenuItem("Paste Claude auth from clipboard", "Finish the Claude OAuth flow by reading the code from the clipboard")
	m.pasteClaude.Hide()
	m.channelsParent = systray.AddMenuItem("My channels", "Recently mounted seller channels (most recent first)")
	m.channelsParent.Hide()
	m.channelChildren = make([]*systray.MenuItem, channelSubmenuSlots)
	for i := range m.channelChildren {
		// Pre-allocated submenu items — title/visibility mutated as
		// the channel list refreshes. systray builds the submenu
		// from the parent, so we add children on the parent now and
		// keep handles for later updates.
		m.channelChildren[i] = m.channelsParent.AddSubMenuItem("", "")
		m.channelChildren[i].Hide()
	}
	m.manageChannels = systray.AddMenuItem("Manage channels (dashboard)…", "Open the dashboard's channels page (anti-phishing verified)")
	m.manageChannels.Hide()
	m.topup = systray.AddMenuItem("Top up / withdraw…", "Open the dashboard wallet (anti-phishing verified)")
	m.topup.Hide()

	systray.AddSeparator()

	m.refreshNow = systray.AddMenuItem("Refresh now", "Re-fetch account info")
	m.refreshNow.Hide()
	m.openWeb = systray.AddMenuItem("Open dashboard…", "Open the EveryAPI web dashboard")
	m.signOut = systray.AddMenuItem("Sign out", "Remove credentials from this device")
	m.signOut.Hide()

	systray.AddSeparator()

	// Help submenu: always visible (no login required), nestled
	// before Quit so it's findable without scrolling past auth-
	// gated items.
	m.helpParent = systray.AddMenuItem("Help", "Documentation, config, and bug reports")
	m.about = m.helpParent.AddSubMenuItem("About EveryAPI", "Show version, commit, license")
	m.openDocs = m.helpParent.AddSubMenuItem("Open documentation…", "Open the docs site in your browser")
	m.revealConfig = m.helpParent.AddSubMenuItem("Reveal config folder…", "Show ~/.config/everyapi in the file browser")
	m.reportIssue = m.helpParent.AddSubMenuItem("Report an issue…", "File a bug on GitHub")
	m.preferences = m.helpParent.AddSubMenuItem("Preferences…", "Edit menubar-prefs.json in your text editor (restart to apply)")

	systray.AddSeparator()

	m.quit = systray.AddMenuItem("Quit EveryAPI", "Exit the menu-bar app")
	return m
}

// applyLoggedOut shows the sign-in path, hides logged-in-only items.
func (m *menuItems) applyLoggedOut() {
	m.username.SetTitle("Not signed in")
	m.quota.SetTitle("Quota: —")
	m.used.SetTitle("Used: —")
	m.requests.SetTitle("Requests: —")
	m.seller.Hide()

	m.signIn.Show()
	m.signIn.Enable()
	m.loginCode.Hide()
	m.copyRelayKey.Hide()
	m.sanitizer.Hide()
	m.addClaude.Hide()
	m.addGemini.Hide()
	m.pasteClaude.Hide()
	m.channelsParent.Hide()
	for _, ch := range m.channelChildren {
		ch.Hide()
	}
	m.manageChannels.Hide()
	m.topup.Hide()
	m.refreshNow.Hide()
	m.signOut.Hide()
}

// applyLoggingIn freezes the sign-in path and surfaces the code.
func (m *menuItems) applyLoggingIn(userCode string) {
	m.signIn.SetTitle("Approve in browser…")
	m.signIn.Disable()
	m.loginCode.SetTitle("Code: " + userCode)
	m.loginCode.Show()
}

// applyLoggedIn flips to the data-display layout.
func (m *menuItems) applyLoggedIn(username string) {
	if username == "" {
		username = "Signed in"
	}
	m.username.SetTitle(username)

	m.signIn.Hide()
	m.signIn.SetTitle("Sign in…") // reset so future logout state is clean
	m.signIn.Enable()
	m.loginCode.Hide()
	m.copyRelayKey.Show()
	m.sanitizer.Show()
	m.addClaude.Show()
	m.addGemini.Show()
	// pasteClaude stays hidden — only shows during an in-flight Claude flow
	// channelsParent stays hidden until the first applyChannels call
	m.manageChannels.Show()
	m.topup.Show()
	m.refreshNow.Show()
	m.signOut.Show()
}

// applyStale shows / hides the "connection lost" warning at the
// top of the menu. Called by the controller after N consecutive
// failed refreshes; cleared on the first success.
func (m *menuItems) applyStale(stale bool) {
	if stale {
		m.staleWarn.Show()
	} else {
		m.staleWarn.Hide()
	}
}

// applyClaudePastePending toggles the visibility of the
// "Paste Claude auth from clipboard" item. Visible only while the
// Claude OAuth flow is between /start and /complete.
func (m *menuItems) applyClaudePastePending(pending bool) {
	if pending {
		m.pasteClaude.Show()
	} else {
		m.pasteClaude.Hide()
	}
}

// applyChannels populates the "My channels" submenu. Up to
// channelSubmenuSlots items are shown; the rest are folded under
// "Manage channels (dashboard)…". Hides the parent when the list
// is empty.
func (m *menuItems) applyChannels(channels []channelMenuRow) {
	if len(channels) == 0 {
		m.channelsParent.Hide()
		for _, ch := range m.channelChildren {
			ch.Hide()
		}
		return
	}
	m.channelsParent.Show()
	for i, slot := range m.channelChildren {
		if i < len(channels) {
			slot.SetTitle(channels[i].Title)
			slot.SetTooltip(channelStatusLabel(channels[i].Status))
			slot.Show()
		} else {
			slot.Hide()
		}
	}
}

// channelStatusLabel renders the human-readable name for a status
// enum. Used as the submenu item's tooltip.
func channelStatusLabel(status int) string {
	switch status {
	case channelStatusEnabled:
		return "Enabled"
	case channelStatusManualDisable:
		return "Manually disabled"
	case channelStatusAutoDisable:
		return "Auto-disabled by health worker — investigate in dashboard"
	default:
		return "Unknown status"
	}
}

// applySanitizerState updates the sanitizer toggle's title +
// checkmark to match the runner. The title varies so users at a
// glance can see the bind address while running ("Sanitizer:
// 127.0.0.1:8888") and a clean "off" while stopped.
func (m *menuItems) applySanitizerState(running bool, listen string) {
	if running {
		title := "Sanitizer: on"
		if listen != "" {
			title = "Sanitizer: " + listen
		}
		m.sanitizer.SetTitle(title)
		m.sanitizer.Check()
	} else {
		m.sanitizer.SetTitle("Sanitizer proxy: off")
		m.sanitizer.Uncheck()
	}
}

// applyData refreshes the quota / usage / requests labels.
func (m *menuItems) applyData(quotaUSD, usedUSD string, requests int64, sellerUSD string, hasSeller bool) {
	m.quota.SetTitle("Quota: " + quotaUSD + " remaining")
	m.used.SetTitle("Used: " + usedUSD)
	m.requests.SetTitle(formatRequests(requests))
	if hasSeller {
		m.seller.SetTitle("Seller earnings: " + sellerUSD)
		m.seller.Show()
	} else {
		m.seller.Hide()
	}
}

func formatRequests(n int64) string {
	return "Requests: " + formatInt(n)
}

// formatInt formats with thousands separators (1,234,567).
func formatInt(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := ""
	for n >= 1000 {
		s = "," + threeDigits(n%1000) + s
		n /= 1000
	}
	s = itoa(n) + s
	if neg {
		s = "-" + s
	}
	return s
}

func threeDigits(n int64) string {
	r := []byte{'0', '0', '0'}
	for i := 2; i >= 0; i-- {
		r[i] = byte('0' + n%10)
		n /= 10
	}
	return string(r)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
