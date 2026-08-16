// Package styletest exposes test helpers for code that depends on lipgloss's process-global color profile. Kept separate from `internal/style` so the production binary doesn't link a `testing` import; importable from any `_test.go` file (e.g. `style_test`, the root `main` package's tests).
package styletest

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// WithColorProfile captures the current lipgloss color profile, sets it to p for the duration of the test, and restores it on cleanup.
//
// The captured-restore pattern is deliberate: resetting to Ascii unconditionally (the older inline pattern) would clobber a non-Ascii default a sibling test had set earlier — leaking state across the suite when tests run in non-default order.
func WithColorProfile(t *testing.T, p termenv.Profile) {
	t.Helper()
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(p)
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })
}
