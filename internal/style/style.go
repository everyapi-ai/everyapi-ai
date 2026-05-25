// Package style centralizes terminal text styling for the CLI: bold
// emphasis for help/menu text. Rendering goes through lipgloss, which
// detects the output profile (TrueColor / 256 / 16 / Ascii), honors
// NO_COLOR, downgrades on non-TTY / piped output, and enables VT
// processing on Windows — so callers never hand-roll ANSI or TTY checks.
package style

import (
	"regexp"

	"github.com/charmbracelet/lipgloss"
)

// emphMarkerRe matches **bold** spans, mirroring the markdown-bold
// convention cmd/update.go already parses for release notes. Non-greedy
// so multiple spans on one line each render independently.
var emphMarkerRe = regexp.MustCompile(`\*\*(.+?)\*\*`)

var boldStyle = lipgloss.NewStyle().Bold(true)

// Bold renders s in bold, inheriting the terminal's default foreground
// (no hardcoded color — readable on dark and light themes alike).
// Returns s unchanged when the output profile carries no styling.
func Bold(s string) string {
	return boldStyle.Render(s)
}

// Emph converts **bold** markers in s to bold text, and strips the
// markers to plain text when styling is unavailable. Apply to any
// user-facing string whose keywords are marked with **…**.
func Emph(s string) string {
	return emphMarkerRe.ReplaceAllStringFunc(s, func(m string) string {
		return Bold(m[len("**") : len(m)-len("**")])
	})
}
