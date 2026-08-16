// Package cliout holds the small set of helpers shared by every CLI subcommand: the stdout writer (swappable from tests), formatted print wrappers, and the context constructors used by interactive vs. fire-and-forget commands. These used to live unexported in the `cmd` package, but lifting them to a separate package lets the per-command subpackages (cmd/seller, cmd/proxy) reach them without the awkward cmd → cmd/* import direction.
package cliout

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Out is the writer every command prints to. Tests swap it for a bytes.Buffer / io.Discard; production points at os.Stdout. Kept as a package var (not a parameter on every function) to keep handlers readable — commands are 99% of the time writing to stdout.
//
// Typed as io.Writer (not *os.File) so tests can plug in anything satisfying the interface without an os.Pipe round-trip.
var Out io.Writer = os.Stdout

// Printf is a thin wrapper around fmt.Fprintf(Out, ...) so handlers don't repeat the Out arg.
func Printf(format string, args ...any) {
	fmt.Fprintf(Out, format, args...)
}

// Println writes a single line to Out (with a trailing newline).
func Println(s string) {
	fmt.Fprintln(Out, s)
}

// WithCtx returns the standard context for a short, fire-and-forget API call. Long-running INTERACTIVE commands (login poll, OAuth wait) must use SignalCtx instead so Ctrl+C unwinds cleanly.
func WithCtx() context.Context {
	return context.Background()
}

// SignalCtx returns a context canceled on the first SIGINT/SIGTERM. Use it (with `defer stop()`) for long-running interactive commands so Ctrl+C cancels the in-flight network call and unwinds cleanly instead of hard-killing the process mid-exchange.
func SignalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Sanitize neutralizes an untrusted, server- or peer-sourced string before it is printed to the user's terminal. Backend-relayed values (DM bodies, usernames, log content/model names, abuse-report fields, error messages) can carry attacker-chosen bytes — left raw, an embedded ESC/CSI/OSC sequence would drive the reader's terminal (cursor moves, line hiding, title/clipboard writes, display spoofing). It drops C0 control bytes (<0x20, except tab), DEL and C1 controls (0x7F-0x9F), and strips ANSI escape sequences (CSI, OSC, and other ESC-introduced forms), leaving ordinary text — including multi-byte UTF-8 — untouched so the display width of normal content is unchanged. Apply any trusted style.* wrapping AFTER sanitizing, never before, or the legitimate escapes are stripped too.
func Sanitize(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b: // ESC — start of an escape sequence
			i = skipEscape(s, i)
		case c < 0x20: // C0 controls — keep tab; fold CR/LF to a space so a
			// multi-line untrusted field renders readably on one line (its lines were otherwise concatenated with no separator) while still collapsing to a single line; drop the rest.
			switch c {
			case '\t':
				b.WriteByte(c)
			case '\n', '\r':
				b.WriteByte(' ')
			}
			i++
		case c < 0x80: // remaining printable ASCII (drop DEL)
			if c != 0x7f {
				b.WriteByte(c)
			}
			i++
		default:
			// Non-ASCII: decode the rune so a raw C1 control (U+0080-U+009F) is dropped without mistaking a UTF-8 continuation byte of a legitimate multi-byte rune for one.
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				i++ // drop a stray invalid byte
				continue
			}
			if r >= 0x80 && r <= 0x9f { // C1 controls
				i += size
				continue
			}
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

// skipEscape consumes the ANSI escape sequence that begins at s[i] (where s[i] == ESC) and returns the index just past it. CSI (ESC [) runs to a final byte in 0x40-0x7E; OSC (ESC ]) runs to BEL or ST (ESC \); any other ESC form drops the ESC plus its single following byte.
func skipEscape(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return j // lone trailing ESC
	}
	switch s[j] {
	case '[': // CSI: ESC [ params/intermediates … final (0x40-0x7E)
		j++
		for j < len(s) {
			b := s[j]
			j++
			if b >= 0x40 && b <= 0x7e {
				break
			}
		}
		return j
	case ']': // OSC: ESC ] … (BEL | ST)
		j++
		for j < len(s) {
			if s[j] == 0x07 { // BEL terminator
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' { // ST: ESC \
				return j + 2
			}
			j++
		}
		return j
	default:
		// ESC <single byte> (e.g. ESC c reset) and other short forms.
		return j + 1
	}
}
