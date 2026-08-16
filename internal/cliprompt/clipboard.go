package cliprompt

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// CopyToClipboard puts text on the system clipboard via the platform's native command-line tool: pbcopy on macOS, clip on Windows, and one of wl-copy / xclip / xsel on Linux / BSD (probed in that order — the first installed wins). Returns a descriptive error if no tool is available; the caller is expected to surface it, since the URL is already printed on screen and the user can still copy it by hand.
func CopyToClipboard(s string) error {
	name, args, err := clipboardCommand()
	if err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

// clipboardCommand picks the platform's clipboard binary. Linux/BSD try Wayland first (wl-copy) because users on Wayland often have xclip / xsel pointing at the unused X11 selection — wl-copy is the only one that lands in the active clipboard.
func clipboardCommand() (string, []string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "pbcopy", nil, nil
	case "windows":
		return "clip", nil, nil
	default:
		candidates := []struct {
			name string
			args []string
		}{
			{"wl-copy", nil},
			{"xclip", []string{"-selection", "clipboard"}},
			{"xsel", []string{"--clipboard", "--input"}},
		}
		// wl-copy only works under a running Wayland compositor. On an X11 session that merely has wl-clipboard installed, wl-copy is present but fails to connect ("failed to connect to a Wayland server"), and we'd wrongly report a copy failure though xclip/xsel would have worked. Skip it unless WAYLAND_DISPLAY is set.
		wayland := os.Getenv("WAYLAND_DISPLAY") != ""
		for _, c := range candidates {
			if c.name == "wl-copy" && !wayland {
				continue
			}
			if _, err := exec.LookPath(c.name); err == nil {
				return c.name, c.args, nil
			}
		}
		return "", nil, fmt.Errorf("no clipboard tool found (install wl-copy, xclip, or xsel)")
	}
}
