package style_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/everyapi-ai/everyapi-ai/internal/style"
)

// withColorProfile captures the current lipgloss color profile,
// sets it to p for the duration of the test, and restores it on
// cleanup. Resetting to Ascii unconditionally (the original pattern)
// would clobber a non-Ascii default set by a sibling test that
// happened to run earlier — leaking state across the suite.
func withColorProfile(t *testing.T, p termenv.Profile) {
	t.Helper()
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(p)
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })
}

func TestEmph_StyledVsPlain(t *testing.T) {
	// Styling ON: marker becomes bold ANSI, markers gone.
	withColorProfile(t, termenv.TrueColor)
	got := style.Emph("Show current **quota**, usage")
	if !strings.Contains(got, "\x1b[1m") {
		t.Fatalf("want bold SGR, got %q", got)
	}
	if strings.Contains(got, "**") {
		t.Fatalf("markers must be consumed, got %q", got)
	}

	// Styling OFF (pipe / NO_COLOR / dumb): plain text, markers stripped.
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := style.Emph("Show current **quota**, usage"); got != "Show current quota, usage" {
		t.Fatalf("want stripped plain text, got %q", got)
	}
}

func TestBold_PlainWhenUnstyled(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	if got := style.Bold("login"); got != "login" {
		t.Fatalf("want %q, got %q", "login", got)
	}
}
