//go:build windows

package menubar

import (
	"os/exec"
	"strings"
)

// realReadClipboard uses PowerShell's Get-Clipboard cmdlet. -Raw
// returns the entire clipboard content as a single string without
// splitting on newlines; we then strip a trailing CRLF the shell
// pipeline often appends.
func realReadClipboard() (string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive",
		"-Command", "Get-Clipboard -Raw").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// realWriteClipboard pipes `s` into PowerShell's Set-Clipboard. We
// pass via stdin (`Set-Clipboard -Value $input`) to avoid quoting
// pitfalls when the relay key contains shell metacharacters.
func realWriteClipboard(s string) error {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive",
		"-Command", "$input | Set-Clipboard")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}
