package style_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/styletest"
)

func TestEmph_StyledVsPlain(t *testing.T) {
	// Styling ON: marker becomes bold ANSI, markers gone.
	styletest.WithColorProfile(t, termenv.TrueColor)
	got := style.Emph("Show current **quota**, usage")
	if !strings.Contains(got, "\x1b[1m") {
		t.Fatalf("want bold SGR, got %q", got)
	}
	if strings.Contains(got, "**") {
		t.Fatalf("markers must be consumed, got %q", got)
	}

	// Styling OFF (pipe / NO_COLOR / dumb): plain text, markers stripped. Bare SetColorProfile mid-test is fine — the cleanup registered by WithColorProfile above still wins on teardown and restores the original profile. A second WithColorProfile call would also work (LIFO cleanup), just reads more verbose.
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := style.Emph("Show current **quota**, usage"); got != "Show current quota, usage" {
		t.Fatalf("want stripped plain text, got %q", got)
	}
}

func TestBold_PlainWhenUnstyled(t *testing.T) {
	styletest.WithColorProfile(t, termenv.Ascii)
	if got := style.Bold("login"); got != "login" {
		t.Fatalf("want %q, got %q", "login", got)
	}
}

func TestBadge_StyledVsPlain(t *testing.T) {
	// Styled: reverse-video + the tone's color SGR wraps the text.
	styletest.WithColorProfile(t, termenv.TrueColor)
	got := style.Badge(" registered ", style.ToneGreen)
	if !strings.Contains(got, "\x1b[7m") || !strings.Contains(got, "\x1b[32m") {
		t.Fatalf("want reverse + green SGR, got %q", got)
	}
	if !strings.Contains(got, " registered ") {
		t.Fatalf("badge must keep its text, got %q", got)
	}

	// Unstyled: plain text, no escapes — so piped/NO_COLOR output stays readable and column math (style.Width) still works.
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := style.Badge(" registered ", style.ToneRed); got != " registered " {
		t.Fatalf("want plain text, got %q", got)
	}
}

func TestWidth_CJK(t *testing.T) {
	// A wide CJK rune counts as 2 columns; this is why badge/back-row padding uses Width, not len.
	if got := style.Width("未注册"); got != 6 {
		t.Errorf("Width(未注册) = %d, want 6", got)
	}
}

// TestEnabledTracksTheColorProfile: Enabled is the switch the docs renderer uses to decide whether backticks still have a job, so it has to agree with whether Bold actually does anything.
func TestEnabledTracksTheColorProfile(t *testing.T) {
	styletest.WithColorProfile(t, termenv.Ascii)
	if style.Enabled() {
		t.Error("Enabled() is true on an Ascii profile")
	}
	if style.Bold("x") != "x" {
		t.Error("Bold styled text on an Ascii profile — Enabled and Bold disagree")
	}
	styletest.WithColorProfile(t, termenv.TrueColor)
	if !style.Enabled() {
		t.Error("Enabled() is false on a TrueColor profile")
	}
	if style.Bold("x") == "x" {
		t.Error("Bold left text unstyled on a TrueColor profile — Enabled and Bold disagree")
	}
}
