//go:build darwin

package menubar

import (
	"os/exec"
	"strings"
)

// realWriteClipboard pipes `s` into pbcopy, which sets the macOS
// general pasteboard. Trailing newline is intentionally omitted —
// pbcopy stores stdin verbatim, and our relay-key consumers don't
// want a trailing \n in their config.
func realWriteClipboard(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

// readClipboard returns the current pasteboard text. Implemented via
// pbpaste, which ships on every macOS install. Trailing newlines are
// trimmed — the Anthropic callback page produces a `code#state`
// string that the user copies; preserving a stray newline would
// fail the backend's input parse.
func realReadClipboard() (string, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
