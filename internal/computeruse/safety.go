package computeruse

import (
	"fmt"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/sanitizer"
)

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

func blockedAppError(app App) error {
	reason, blocked := knownBlockedBundleIDs[strings.ToLower(app.BundleID)]
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
	return state
}
