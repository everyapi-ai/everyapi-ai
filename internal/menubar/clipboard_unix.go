//go:build !darwin && !windows

package menubar

import (
	"errors"
	"os/exec"
	"strings"
)

// errClipboardUnsupported surfaces when no clipboard helper is on
// PATH. The caller (copy-key handler / Claude paste-back) renders
// the error as a notification so the user knows to install one.
var errClipboardUnsupported = errors.New("no clipboard helper found — install xclip (X11), wl-clipboard (Wayland), or xsel")

// realReadClipboard tries the three common Linux clipboard tools in
// order. xclip is the X11 default; wl-paste covers Wayland sessions;
// xsel is a smaller alternative some distros prefer.
func realReadClipboard() (string, error) {
	candidates := []struct {
		bin  string
		args []string
	}{
		{"xclip", []string{"-selection", "clipboard", "-o"}},
		{"wl-paste", []string{"--no-newline"}},
		{"xsel", []string{"--clipboard", "--output"}},
	}
	for _, c := range candidates {
		if path, err := exec.LookPath(c.bin); err == nil {
			out, err := exec.Command(path, c.args...).Output()
			if err != nil {
				return "", err
			}
			return strings.TrimRight(string(out), "\r\n"), nil
		}
	}
	return "", errClipboardUnsupported
}

// realWriteClipboard symmetric to read. We pass the value via stdin
// to avoid shell-quoting pitfalls on long / metacharacter-rich keys.
func realWriteClipboard(s string) error {
	candidates := []struct {
		bin  string
		args []string
	}{
		{"xclip", []string{"-selection", "clipboard"}},
		{"wl-copy", nil},
		{"xsel", []string{"--clipboard", "--input"}},
	}
	for _, c := range candidates {
		if path, err := exec.LookPath(c.bin); err == nil {
			cmd := exec.Command(path, c.args...)
			cmd.Stdin = strings.NewReader(s)
			return cmd.Run()
		}
	}
	return errClipboardUnsupported
}
