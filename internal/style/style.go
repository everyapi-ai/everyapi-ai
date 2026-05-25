// Package style centralizes terminal text styling for the CLI: bold
// emphasis for help/menu text. Rendering goes through lipgloss, which
// detects the output profile (TrueColor / 256 / 16 / Ascii), honors
// NO_COLOR, downgrades on non-TTY / piped output, and enables VT
// processing on Windows — so callers never hand-roll ANSI or TTY checks.
package style

import (
	"regexp"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// emphMarkerRe matches **bold** spans, mirroring the markdown-bold
// convention cmd/update.go already parses for release notes. Non-greedy
// so multiple spans on one line each render independently.
var emphMarkerRe = regexp.MustCompile(`\*\*(.+?)\*\*`)

// Bold renders s in bold, inheriting the terminal's default foreground
// (no hardcoded color — readable on dark and light themes alike).
// Returns s unchanged when the output has no styling (piped / NO_COLOR /
// dumb terminal); lipgloss.ColorProfile reports the stdout profile and
// enables VT processing on Windows.
//
// The span CLOSES with \x1b[22m (bold-off), NOT \x1b[0m (full reset).
// These labels get embedded inside huh's per-row foreground style for
// the launcher picker; a full reset would strip that selection
// highlight for the rest of the line, whereas bold-off turns off only
// the intensity and leaves any surrounding color intact.
func Bold(s string) string {
	if lipgloss.ColorProfile() == termenv.Ascii {
		return s
	}
	return "\x1b[1m" + s + "\x1b[22m"
}

// Emph converts **bold** markers in s to bold text, and strips the
// markers to plain text when styling is unavailable. Apply to any
// user-facing string whose keywords are marked with **…**.
func Emph(s string) string {
	return emphMarkerRe.ReplaceAllStringFunc(s, func(m string) string {
		return Bold(m[len("**") : len(m)-len("**")])
	})
}
