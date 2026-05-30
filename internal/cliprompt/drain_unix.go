//go:build !windows

package cliprompt

import (
	"os"
	"syscall"

	"golang.org/x/term"
)

// DrainStdin discards any bytes already sitting in stdin's buffer.
//
// It exists for one specific handoff: an interactive huh prompt (the
// model / tool / group pickers) runs on bubbletea, whose lipgloss theme
// asks the terminal for its background color via an OSC 11 query. The
// terminal's reply (`\e]11;rgb:rrrr/gggg/bbbb\a`) often lands in the
// input buffer *after* huh has restored the terminal and returned — so
// nothing reads it. When `everyapi use` then execs the target tool, that
// stray reply becomes the tool's first "keystrokes", surfacing as
// `]11;rgb:..` before the user's real input. Draining right before exec
// throws the reply away so the tool starts on a clean line.
//
// No-op when stdin isn't a TTY (piped / CI). Restores blocking mode
// before returning, so the fd inherited by the exec'd child is exactly
// as it was — a non-blocking stdin would break the tool.
func DrainStdin() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		return
	}
	defer func() { _ = syscall.SetNonblock(fd, false) }()

	buf := make([]byte, 256)
	for {
		n, err := syscall.Read(fd, buf)
		if err == syscall.EINTR {
			continue
		}
		// EAGAIN (buffer empty), any other error, or a 0-length read
		// all mean "nothing more to discard".
		if n <= 0 || err != nil {
			return
		}
	}
}
