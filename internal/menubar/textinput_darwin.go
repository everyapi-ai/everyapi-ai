//go:build darwin

package menubar

import (
	"errors"
	"os/exec"
	"strings"
)

// textPrompt shows a single-line text input modal via osascript and
// returns the typed string and a confirmed bool. Cancellation
// (Cancel button or Esc) returns ("", false, nil) — distinguishable
// from an empty confirmed entry by the boolean.
//
// AppleScript's `display dialog … default answer ""` returns a string
// that contains both the chosen button and the entered text on
// success ("button returned:OK, text returned:foo"). We parse the
// text portion; a missing text-returned field maps to empty input.
func realTextPrompt(title, body, defaultValue string) (string, bool, error) {
	script := `display dialog "` + osaEscape(body) + `" ` +
		`with title "` + osaEscape(title) + `" ` +
		`default answer "` + osaEscape(defaultValue) + `" ` +
		`buttons {"Cancel", "OK"} default button "OK" cancel button "Cancel"`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", false, nil
		}
		return "", false, err
	}
	s := string(out)
	const marker = "text returned:"
	idx := strings.Index(s, marker)
	if idx < 0 {
		return "", false, nil
	}
	text := strings.TrimRight(s[idx+len(marker):], "\r\n")
	// Trailing fields are comma-separated when osascript appends
	// more attributes, but `display dialog` puts text last, so this
	// is robust to the common shape.
	return text, true, nil
}
