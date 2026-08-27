package computeruse

import (
	"fmt"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/sanitizer"
)

// Browsers hold the user's logged-in sessions for every site they have ever signed into, so driving one is equivalent to acting as that user against arbitrary services — without any of those services seeing an EveryAPI credential or an audit trail. Reading is blocked on the same footing as clicking: an accessibility snapshot of a browser window returns page text from whatever authenticated session is open.
const browserBlockReason = "web browsers are blocked because they carry the user's authenticated sessions to arbitrary sites"

var knownBlockedBundleIDs = map[string]string{
	"ai.everyapi.connect":           "EveryAPI Connect cannot control its own permission and credential surface",
	"com.apple.terminal":            "terminal applications are blocked because they bypass shell execution controls",
	"com.googlecode.iterm2":         "terminal applications are blocked because they bypass shell execution controls",
	"dev.warp.warp-stable":          "terminal applications are blocked because they bypass shell execution controls",
	"com.mitchellh.ghostty":         "terminal applications are blocked because they bypass shell execution controls",
	"org.alacritty":                 "terminal applications are blocked because they bypass shell execution controls",
	"net.kovidgoyal.kitty":          "terminal applications are blocked because they bypass shell execution controls",
	"com.github.wez.wezterm":        "terminal applications are blocked because they bypass shell execution controls",
	"co.zeit.hyper":                 "terminal applications are blocked because they bypass shell execution controls",
	"com.raphaelamorim.rio":         "terminal applications are blocked because they bypass shell execution controls",
	"com.1password.1password":       "password managers are blocked",
	"com.agilebits.onepassword7":    "password managers are blocked",
	"com.bitwarden.desktop":         "password managers are blocked",
	"org.keepassxc.keepassxc":       "password managers are blocked",
	"com.dashlane.dashlane":         "password managers are blocked",
	"com.markmcguill.strongbox":     "password managers are blocked",
	"com.markmcguill.strongbox.mac": "password managers are blocked",
	"me.proton.pass":                "password managers are blocked",
	"in.sinew.enpass-desktop":       "password managers are blocked",
	"com.lastpass.lastpass":         "password managers are blocked",
	"com.apple.keychainaccess":      "Keychain Access is blocked",
	"com.apple.passwords":           "Passwords is blocked",
	"com.apple.systempreferences":   "System Settings is blocked because it owns privacy permissions",
}

// Browsers that ship a single build, or whose channels do not hang off the stable identifier, so `blockedBundlePrefixes` cannot reach them. Kept separate from `knownBlockedBundleIDs` because this list is long, is maintained against a different source, and shares one reason.
//
// Identifiers are taken from each cask's `uninstall quit:` / `zap trash:` declarations in Homebrew Cask, which is the closest thing to a maintained registry of real macOS bundle identifiers; `com.tencent.QQBrowser` comes from Tencent's own lemon-cleaner. Android package names read exactly like bundle identifiers, so anything sourced from a mobile app list was rejected rather than guessed at.
var knownBlockedBrowserBundleIDs = map[string]bool{
	// Opera is enumerated per channel on purpose: `com.operasoftware.Opera` as a prefix also matches `com.operasoftware.OperaMail`, a mail client.
	"com.operasoftware.opera":          true,
	"com.operasoftware.operagx":        true,
	"com.operasoftware.operaair":       true,
	"com.operasoftware.operanext":      true,
	"com.operasoftware.operadeveloper": true,
	// Firefox forks. Nightly drops the `firefox` infix, and several forks nest themselves under `org.mozilla.com.` instead — see the prefix table.
	"org.mozilla.nightly":                     true,
	"net.waterfox.waterfox":                   true,
	"org.waterfoxproject.waterfox":            true,
	"io.gitlab.librewolf-community.librewolf": true,
	"net.librewolf.librewolf":                 true,
	"net.mullvad.mullvadbrowser":              true,
	"app.zen-browser.zen":                     true,
	"app.glide-browser.glide":                 true,
	// Chromium forks that do not keep the `org.chromium.Chromium` identifier.
	"org.chromium.thorium":          true,
	"ru.cryptopro.chromium-gost":    true,
	"com.avast.browser":             true,
	"com.avast.avastsecurebrowser":  true,
	"com.alohabrowser.alohabrowser": true,
	"com.browseros.browseros":       true,
	"com.hiddenreflex.epic":         true,
	"com.ghostbrowser.gb1":          true,
	"net.imput.helium":              true,
	"de.iridiumbrowser":             true,
	"org.blisk.blisk":               true,
	"com.donutbrowser":              true,
	"at.studio.asidebrowser":        true,
	"com.talon-sec.work":            true,
	// AI browsers. These are the newest arrivals and the likeliest thing to be missing here.
	"ai.perplexity.comet": true,
	"com.openai.atlas":    true,
	// Everything else.
	"com.kagi.kagimacos":               true,
	"org.torproject.torbrowser":        true,
	"com.duckduckgo.macos.browser":     true,
	"ru.yandex.desktop.yandex-browser": true,
	"com.naver.whale":                  true,
	"com.tencent.qqbrowser":            true,
	"com.sigmaos.sigmaos.macos":        true,
	"org.115browser.115browser":        true,
	"de.icab.icab":                     true,
	"com.electron.min":                 true,
	"com.firstversionist.polypane":     true,
	"io.wavebox.wavebox":               true,
	"com.bookry.wavebox":               true,
	"com.webcatalog.singlebox2":        true,
}

// Release channels and browser-installed web apps hang off the stable build's identifier — `com.google.Chrome.canary`, `com.google.Chrome.app.<id>`, `com.microsoft.edgemac.Beta`, `com.brave.Browser.nightly`, `com.vivaldi.Vivaldi.snapshot`, `com.apple.SafariTechnologyPreview` — so an exact-match table alone lets a channel build or a PWA shim walk straight around it. Not every channel takes a suffix: Chrome's beta and dev builds and Firefox's beta and ESR builds all ship under the stable identifier, which is another reason to match on the prefix rather than enumerate.
//
// Each prefix has to stay narrow enough to name one browser family. `org.mozilla.` on its own would also block Thunderbird, `com.microsoft.` would take out Word, and `com.operasoftware.Opera` really does match Opera Mail — so Opera is enumerated per channel above instead. `org.mozilla.com.` is a separate namespace some Firefox forks nest under (Zen, Glide) and does not collide with Mozilla's own products. Keys are compared lowercased, so every prefix here is written lowercase.
var blockedBundlePrefixes = []struct {
	prefix string
	reason string
}{
	{prefix: "com.apple.safari", reason: browserBlockReason},
	{prefix: "com.google.chrome", reason: browserBlockReason},
	{prefix: "org.chromium.chromium", reason: browserBlockReason},
	{prefix: "com.microsoft.edgemac", reason: browserBlockReason},
	{prefix: "org.mozilla.firefox", reason: browserBlockReason},
	{prefix: "org.mozilla.com.", reason: browserBlockReason},
	{prefix: "com.brave.browser", reason: browserBlockReason},
	{prefix: "com.vivaldi.vivaldi", reason: browserBlockReason},
	{prefix: "company.thebrowser.", reason: browserBlockReason},
}

func blockedAppError(app App) error {
	bundleID := strings.ToLower(app.BundleID)
	reason, blocked := knownBlockedBundleIDs[bundleID]
	if !blocked && knownBlockedBrowserBundleIDs[bundleID] {
		reason, blocked = browserBlockReason, true
	}
	if !blocked {
		for _, entry := range blockedBundlePrefixes {
			if strings.HasPrefix(bundleID, entry.prefix) {
				reason, blocked = entry.reason, true
				break
			}
		}
	}
	if !blocked {
		return nil
	}
	return NewError(CodeAppBlocked, fmt.Sprintf("application %q (%s) is blocked: %s", redactSensitiveText(app.Name), redactSensitiveText(app.BundleID), reason), nil)
}

func sensitiveMatches(text string) []sanitizer.Match {
	return sanitizer.Scan(text, sanitizer.BuiltinDetectors())
}

func rejectSensitiveText(text string) error {
	matches := sensitiveMatches(sanitizeObservedText(text))
	if len(matches) == 0 {
		return nil
	}
	names := make([]string, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		if !seen[match.DetectorName] {
			seen[match.DetectorName] = true
			names = append(names, match.DetectorName)
		}
	}
	return NewError(CodeSensitiveText, "refusing to send detected credential text through computer use ("+strings.Join(names, ", ")+")", nil)
}

func redactSensitiveText(text string) string {
	text = sanitizeObservedText(text)
	matches := sensitiveMatches(text)
	if len(matches) == 0 {
		return text
	}
	var b strings.Builder
	cursor := 0
	for _, match := range matches {
		b.WriteString(text[cursor:match.Start])
		b.WriteString("[REDACTED:")
		b.WriteString(match.DetectorName)
		b.WriteByte(']')
		cursor = match.End
	}
	b.WriteString(text[cursor:])
	return b.String()
}

func sanitizeObservedText(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = cliout.Sanitize(lines[i])
	}
	return strings.Join(lines, "\n")
}

func redactState(state State) State {
	state.App.Name = redactSensitiveText(state.App.Name)
	state.App.BundleID = redactSensitiveText(state.App.BundleID)
	state.Window.Title = redactSensitiveText(state.Window.Title)
	state.Snapshot.TreeText = redactSensitiveText(state.Snapshot.TreeText)
	for i := range state.Snapshot.Elements {
		state.Snapshot.Elements[i].Title = redactSensitiveText(state.Snapshot.Elements[i].Title)
		state.Snapshot.Elements[i].Description = redactSensitiveText(state.Snapshot.Elements[i].Description)
		state.Snapshot.Elements[i].Value = redactSensitiveText(state.Snapshot.Elements[i].Value)
	}
	if state.RefreshError != nil {
		state.RefreshError.Message = redactSensitiveText(state.RefreshError.Message)
	}
	if state.ScreenshotError != nil {
		state.ScreenshotError.Message = redactSensitiveText(state.ScreenshotError.Message)
	}
	return state
}
