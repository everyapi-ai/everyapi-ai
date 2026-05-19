//go:build windows

package menubar

import (
	"errors"
	"os/exec"
	"strings"
)

// realTextPrompt on Windows uses the venerable VisualBasic InputBox,
// which PowerShell exposes via the Microsoft.VisualBasic assembly.
// Same shape as the macOS osascript prompt: title / body / default
// value in, single-line text + ok-bool out. Cancellation returns an
// empty string with ok=false.
func realTextPrompt(title, body, defaultValue string) (string, bool, error) {
	// psEscape now handles \n / \r / \t consistently across title /
	// body / default — earlier versions only special-cased body which
	// left the title open to control-char injection.
	script := `Add-Type -AssemblyName Microsoft.VisualBasic | Out-Null;` +
		`$r = [Microsoft.VisualBasic.Interaction]::InputBox(` +
		`'` + psEscape(body) + `',` +
		`'` + psEscape(title) + `',` +
		`'` + psEscape(defaultValue) + `');` +
		`Write-Output $r`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", false, nil
		}
		return "", false, err
	}
	val := strings.TrimRight(string(out), "\r\n")
	// InputBox returns "" both for "user clicked Cancel" and "user
	// confirmed an empty entry". Treat empty as cancellation here —
	// the caller already validates non-empty for OAuth name/models,
	// so a real confirmed-empty would have failed anyway.
	if val == "" {
		return "", false, nil
	}
	return val, true, nil
}
