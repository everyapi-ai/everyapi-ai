//go:build darwin

package menubar

import (
	"errors"
	"os/exec"
	"strings"
)

// confirmDialog shows a macOS native modal with a heading, body, and
// two buttons. Returns true if the user clicked the confirm button.
// Implemented via osascript so the menubar binary doesn't pull in any
// GUI toolkit — `osascript` is part of every macOS install.
//
// The dialog is modal across the user's session: deliberately so for
// the jump-phrase phishing check, where we want the user's attention
// before they touch the browser.
func realConfirmDialog(title, body, confirmLabel, cancelLabel string) (bool, error) {
	if confirmLabel == "" {
		confirmLabel = "OK"
	}
	if cancelLabel == "" {
		cancelLabel = "Cancel"
	}
	// AppleScript-escape: \ and " inside the literal must be escaped.
	// Newlines need to be literal newlines in the script, NOT \n
	// (osascript interprets the latter as a backslash-n in the
	// rendered dialog).
	script := `display dialog "` + osaEscape(body) + `" ` +
		`with title "` + osaEscape(title) + `" ` +
		`buttons {"` + osaEscape(cancelLabel) + `", "` + osaEscape(confirmLabel) + `"} ` +
		`default button "` + osaEscape(confirmLabel) + `" ` +
		`cancel button "` + osaEscape(cancelLabel) + `"`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		// User pressed Cancel → osascript exits 1 with "User
		// canceled. (-128)" on stderr. Distinguishing this from a
		// real failure isn't worth the parse; treat any error as
		// "not confirmed" and surface the original error.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	}
	return strings.Contains(string(out), "button returned:"+confirmLabel), nil
}

// osaEscape escapes a string for inclusion inside AppleScript double
// quotes. Backslash and double-quote get escaped; literal newlines
// break the AppleScript string syntax, so we splice them out as
// concatenations (`" & return & "`). Carriage returns and tabs get
// the same treatment for consistency. NUL bytes are rejected outright
// — they truncate strings in many places and shouldn't appear in any
// legitimate dialog payload.
func osaEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 0:
			// drop NUL; AppleScript handles it badly and no
			// legitimate caller produces one
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`" & return & "`)
		case '\r':
			// most callers pair \r with \n already; treat lone \r
			// the same as \n so a Windows-style CRLF body still
			// renders correctly
			b.WriteString(`" & return & "`)
		case '\t':
			b.WriteString(`" & tab & "`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
