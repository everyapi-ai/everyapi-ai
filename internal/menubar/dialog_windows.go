//go:build windows

package menubar

import (
	"errors"
	"os/exec"
	"strings"
)

// confirmDialog on Windows shells out to PowerShell to render a
// native MessageBox. PowerShell ships with every supported Windows
// release, so this works zero-install. The script returns "OK" or
// "Cancel"; we map those to the confirm bool.
//
// MessageBox's button labels are hard-wired ("OK" / "Cancel"); the
// confirm/cancel label parameters are ignored on Windows. That's
// acceptable because the phrase + warning text live in the body, and
// matching the OS-native button conventions arguably reads more
// trustworthy than custom labels on the anti-phishing flow.
func realConfirmDialog(title, body, confirmLabel, cancelLabel string) (bool, error) {
	// Add.Type lets us touch System.Windows.Forms without dragging
	// in a full Windows Forms host process. The single-line script
	// keeps the PowerShell invocation cheap.
	script := `Add-Type -AssemblyName System.Windows.Forms | Out-Null;` +
		`[System.Windows.Forms.MessageBox]::Show('` + psEscape(body) + `',` +
		`'` + psEscape(title) + `',` +
		`'OKCancel','Information')`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(string(out))), "ok"), nil
}

// psEscape escapes a string for inclusion inside PowerShell single
// quotes. Inside single quotes only the single-quote itself needs
// doubling. Literal newlines inside a -Command argument confuse the
// host's argument parser; we substitute the PowerShell escape `n`
// (backtick-n) which is a real newline inside the dialog text.
// NUL bytes are dropped — they truncate native Win32 strings.
func psEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 0:
			// drop NUL
		case '\'':
			b.WriteString("''")
		case '\r':
			// fold CR into LF — Windows CRLF lines collapse cleanly
			// after the next iteration handles the \n
		case '\n':
			b.WriteString("`n")
		case '\t':
			b.WriteString("`t")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
