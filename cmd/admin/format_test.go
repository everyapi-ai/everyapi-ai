package admin

import (
	"testing"

	"github.com/muesli/termenv"

	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/styletest"
)

// roleLabel / userStatusLabel must translate the known enums and fall
// back to the raw form for anything unexpected (so a future backend role
// is visible, not silently blank). Force the Ascii profile so labels are
// plain (no color escapes) regardless of the test host's TERM.
func TestRoleAndStatusLabels(t *testing.T) {
	styletest.WithColorProfile(t, termenv.Ascii)
	i18n.SetLanguage("en")
	for role, want := range map[int]string{100: "root", 10: "admin", 1: "user", 0: "guest"} {
		if got := roleLabel(role); got != want {
			t.Errorf("roleLabel(%d) = %q, want %q", role, got, want)
		}
	}
	if got := roleLabel(7); got != "role=7" {
		t.Errorf("roleLabel(7) = %q, want raw fallback role=7", got)
	}
	if got := userStatusLabel(1); got != "enabled" {
		t.Errorf("userStatusLabel(1) = %q, want enabled", got)
	}
	if got := userStatusLabel(2); got != "disabled" {
		t.Errorf("userStatusLabel(2) = %q, want disabled", got)
	}
	if got := userStatusLabel(9); got != "status=9" {
		t.Errorf("userStatusLabel(9) = %q, want raw fallback status=9", got)
	}
}

// commaInt groups by threes, including the boundary cases that off-by-one
// grouping bugs trip on.
func TestCommaInt(t *testing.T) {
	cases := map[int64]string{
		0: "0", 5: "5", 99: "99", 100: "100", 999: "999",
		1000: "1,000", 9000000: "9,000,000", 1466981315: "1,466,981,315",
		-1234: "-1,234",
	}
	for in, want := range cases {
		if got := commaInt(in); got != want {
			t.Errorf("commaInt(%d) = %q, want %q", in, got, want)
		}
	}
}

// fmtQuota converts to USD when the divisor is known and falls back to a
// grouped raw count when it isn't (perUnit==0).
func TestFmtQuota(t *testing.T) {
	if got := fmtQuota(9000000, 500000); got != "$18.00" {
		t.Errorf("fmtQuota(9000000, 500000) = %q, want $18.00", got)
	}
	if got := fmtQuota(9000000, 0); got != "9,000,000" {
		t.Errorf("fmtQuota with no perUnit = %q, want raw grouped 9,000,000", got)
	}
}

// padLeft / padName align by display width (CJK runes count as 2), which
// is what keeps the table columns straight.
func TestPad(t *testing.T) {
	if got := padLeft("#1", 4); got != "  #1" {
		t.Errorf("padLeft(#1,4) = %q, want '  #1'", got)
	}
	if got := padName("x", 4); got != "x   " {
		t.Errorf("padName(x,4) = %q, want 'x   '", got)
	}
	// 角色 is 2 CJK runes = 4 display cols; padding to 6 adds 2 spaces.
	if got := padName("角色", 6); got != "角色  " {
		t.Errorf("padName(角色,6) = %q, want '角色  '", got)
	}
}
