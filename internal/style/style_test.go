package style_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/everyapi-ai/everyapi-ai/internal/style"
)

func TestEmph_StyledVsPlain(t *testing.T) {
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	// Styling ON: marker becomes bold ANSI, markers gone.
	lipgloss.SetColorProfile(termenv.TrueColor)
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
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := style.Bold("login"); got != "login" {
		t.Fatalf("want %q, got %q", "login", got)
	}
}
