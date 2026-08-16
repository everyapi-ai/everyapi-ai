// Package style centralizes terminal text styling for the CLI: bold emphasis for help/menu text. Rendering goes through lipgloss, which detects the output profile (TrueColor / 256 / 16 / Ascii), honors NO_COLOR, downgrades on non-TTY / piped output, and enables VT processing on Windows — so callers never hand-roll ANSI or TTY checks.
package style

import (
	"regexp"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// emphMarkerRe matches **bold** spans, mirroring the markdown-bold convention cmd/update.go already parses for release notes. Non-greedy so multiple spans on one line each render independently.
var emphMarkerRe = regexp.MustCompile(`\*\*(.+?)\*\*`)

// Bold renders s in bold, inheriting the terminal's default foreground (no hardcoded color — readable on dark and light themes alike). Returns s unchanged when the output has no styling (piped / NO_COLOR / dumb terminal); lipgloss.ColorProfile reports the stdout profile and enables VT processing on Windows.
//
// The span CLOSES with \x1b[22m (bold-off), NOT \x1b[0m (full reset). These labels get embedded inside huh's per-row foreground style for the launcher picker; a full reset would strip that selection highlight for the rest of the line, whereas bold-off turns off only the intensity and leaves any surrounding color intact.
func Bold(s string) string {
	if lipgloss.ColorProfile() == termenv.Ascii {
		return s
	}
	return "\x1b[1m" + s + "\x1b[22m"
}

// Emph converts **bold** markers in s to bold text, and strips the markers to plain text when styling is unavailable. Apply to any user-facing string whose keywords are marked with **…**.
//
// IMPORTANT — emphasis is opt-in PER CALL SITE, not per locale key. `Emph` is currently wired into the launcher / help rendering path (renderUsage, commandDesc, subcommandDesc, and the nameCell-via- Bold pickers). i18n keys rendered through OTHER paths (e.g. `update.notice` in cmd/update_check.go is formatted via `fmt.Sprintf(i18n.T(...), ...)` with no Emph pass) will surface `**` as literal asterisks if marked.
//
// Adding `**…**` to a locale value WITHOUT also routing the call site through `Emph` is the failure mode. TestLocaleMarkersBalanced guards marker balance but cannot detect "marked key not routed through Emph" — that needs reviewer attention at the call site when introducing emphasis to a new key family.
func Emph(s string) string {
	return emphMarkerRe.ReplaceAllStringFunc(s, func(m string) string {
		return Bold(m[len("**") : len(m)-len("**")])
	})
}

// Width returns the printable display width of s — CJK / wide runes count as 2 columns, ANSI escapes as 0. Use it (not len) when aligning columns that may contain non-ASCII text (e.g. a localized menu label).
func Width(s string) int {
	return lipgloss.Width(s)
}

// Tone selects a Badge's color.
type Tone int

const (
	ToneGreen  Tone = iota // success / present
	ToneYellow             // attention / action needed
	ToneRed                // error / failure
	ToneGray               // absent / inactive
)

func (t Tone) sgr() string {
	switch t {
	case ToneYellow:
		return "\x1b[33m"
	case ToneRed:
		return "\x1b[31m"
	case ToneGray:
		return "\x1b[90m"
	default:
		return "\x1b[32m"
	}
}

// Badge renders s as a reverse-video colored chip (the foreground color becomes the chip's fill). TTY-aware: returns s unchanged when output is unstyled (NO_COLOR / piped / dumb terminal), so callers still get readable text. Pad s to a fixed display width before calling when aligning a column of badges.
func Badge(s string, t Tone) string {
	if lipgloss.ColorProfile() == termenv.Ascii {
		return s
	}
	return "\x1b[7m" + t.sgr() + s + "\x1b[0m"
}

// Color renders s in the tone's foreground color (no reverse-video) — lighter than Badge, for tinting table cells (status / role) without a chip on every row. Same TTY/NO_COLOR gate; Width still measures the visible text, so colored cells stay aligned.
func Color(s string, t Tone) string {
	if lipgloss.ColorProfile() == termenv.Ascii {
		return s
	}
	return t.sgr() + s + "\x1b[0m"
}

// Dim renders s faint — for de-emphasized chrome like table headers and id columns. TTY-aware; closes with a full reset.
func Dim(s string) string {
	if lipgloss.ColorProfile() == termenv.Ascii {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}
