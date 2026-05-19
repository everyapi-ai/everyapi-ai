// Package menubar implements the GUI menu-bar surface (M2+). It is a
// sibling to the CLI command packages in cmd/, wrapping the same
// internal/api + internal/config + internal/sanitizer + ... packages
// with a long-running goroutine-driven controller. Imported by
// cmd/menubar/main.go.
package menubar

import (
	"fmt"
	"net/url"
)

// formatUSD renders an integer quota field as a "$X.XX" string given
// the quota_per_unit divisor from /api/status. Matches the CLI's
// status command output so users see the same numbers on either
// surface. Negative or zero perUnit falls back to raw integer display
// (defensive — the server should always send a positive value).
func formatUSD(quota int64, perUnit float64) string {
	if perUnit <= 0 {
		return fmt.Sprintf("%d", quota)
	}
	return fmt.Sprintf("$%.2f", float64(quota)/perUnit)
}

// notifyBodyMaxLen caps how much of an error / arbitrary string we
// surface in a desktop notification body. The notification banner
// is rendered by the OS at sign-share / screen-record visibility,
// so adversarial payloads (clipboard contents echoed by backend
// error messages, OAuth provider error_description, ...) get clipped
// before display. 240 chars is about three lines on a standard
// macOS / Windows / Linux toast.
const notifyBodyMaxLen = 240

// truncateForNotify returns s capped at notifyBodyMaxLen with an
// "…" tail when truncation happens. Empty input passes through.
func truncateForNotify(s string) string {
	if len(s) <= notifyBodyMaxLen {
		return s
	}
	// Slice on a rune boundary so we don't bisect a multibyte char
	// (most error strings are ASCII but defensive doesn't hurt).
	r := []rune(s)
	if len(r) <= notifyBodyMaxLen {
		return s
	}
	return string(r[:notifyBodyMaxLen]) + "…"
}

// buildVerificationURLWithCode glues a user_code onto a verification
// URI as a `?code=` query parameter. The dashboard /cli/auth page
// auto-fills the input field when present, so a browser opened with
// this URL goes straight to the confirm screen.
//
// Mirrors cmd/qrurl.go:buildVerificationURLWithCode (kept in cmd/
// because the test lives there); duplicated here as 10 lines rather
// than spinning up a third package just for URL glue.
func buildVerificationURLWithCode(verificationURI, userCode string) string {
	if verificationURI == "" || userCode == "" {
		return verificationURI
	}
	u, err := url.Parse(verificationURI)
	if err != nil {
		return verificationURI
	}
	q := u.Query()
	q.Set("code", userCode)
	u.RawQuery = q.Encode()
	return u.String()
}
