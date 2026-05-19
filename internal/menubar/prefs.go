package menubar

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"os/exec"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// preferences mirrors menubar-prefs.json. Every field is optional;
// zero values preserve the built-in defaults (commented out in the
// seed payload below). Keeping the file optional means a user who
// never opens Preferences pays nothing for the feature.
//
// Editing is via the user's default text editor (M15 design) — no
// in-app preferences GUI. Changes take effect on next launch; the
// menu shows a "restart to apply" notification when the file is
// touched.
type preferences struct {
	RefreshIntervalSeconds int    `json:"refresh_interval_seconds,omitempty"`
	SanitizerListen        string `json:"sanitizer_listen,omitempty"`
	MuteEarnings           bool   `json:"mute_earnings_notifications,omitempty"`
	MuteRisk               bool   `json:"mute_risk_notifications,omitempty"`
	APIBase                string `json:"api_base,omitempty"`
}

// prefsSeed is what we write when the file doesn't exist yet. The
// commented fields document every available knob without overriding
// any default — users uncomment + edit only what they care about.
const prefsSeed = `{
  "_README": "EveryAPI menubar preferences. Uncomment + edit a value, save, then restart the menubar.",
  "_refresh_interval_seconds": 30,
  "_sanitizer_listen": "127.0.0.1:8888",
  "_mute_earnings_notifications": false,
  "_mute_risk_notifications": false,
  "_api_base": "https://api.everyapi.ai (overrides the default at sign-in time; ignored once credentials.json exists)"
}
`

func prefsPath() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "menubar-prefs.json"), nil
}

// loadPrefs returns the saved preferences, or a zero value when the
// file is missing / unreadable. We deliberately swallow JSON errors
// (a malformed file produces zero prefs, which is the same as
// "no prefs") — surfacing those would require a notification, and
// the user already has the file open in their editor when the parse
// fails. Better to ship safer defaults than to crash.
func loadPrefs() preferences {
	path, err := prefsPath()
	if err != nil {
		return preferences{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return preferences{}
	}
	var p preferences
	_ = json.Unmarshal(data, &p)
	return p
}

// ensurePrefsFile writes the seed payload if no file exists, so the
// "Preferences…" menu item opens something useful even on a fresh
// install.
func ensurePrefsFile() (string, error) {
	path, err := prefsPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	dir, _ := config.ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(prefsSeed), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// openInEditorFn is the package-var indirection for the editor
// shell-out, swappable in tests.
var openInEditorFn = realOpenInEditor

// realOpenInEditor opens the file in the user's default text
// editor. Same approach as `git config --edit`: don't bundle a UI,
// lean on what the OS knows the user already prefers.
func realOpenInEditor(path string) error {
	switch runtime.GOOS {
	case "darwin":
		// `open -t` routes to whatever app is registered for plain
		// text — TextEdit by default, BBEdit / VS Code / etc. when
		// the user has changed it.
		return exec.Command("open", "-t", path).Start()
	case "windows":
		return exec.Command("notepad", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
