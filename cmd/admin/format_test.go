package admin

import (
	"strings"
	"testing"

	"github.com/muesli/termenv"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/styletest"
	"github.com/everyapi-ai/everyapi-sdk/api"
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

// sanitize must neutralize attacker-influenceable backend strings before
// they reach the operator's terminal: strip ANSI escape sequences and drop
// control bytes, while leaving ordinary (incl. multi-byte UTF-8) text — and
// tabs — intact so column widths are unchanged.
func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mX\x1b[0m":    "X",        // CSI color escapes stripped
		"a\x07b":              "ab",       // BEL (C0) dropped
		"\x1b]0;pwn\x07tail":  "tail",     // OSC title set + BEL terminator
		"\x1b]0;pwn\x1b\\end": "end",      // OSC + ST (ESC \) terminator
		"plain":               "plain",    // untouched
		"col\tval":            "col\tval", // tab preserved
		"a\x1b[2Kb":           "ab",       // CSI erase-line stripped
		"mid":                "mid",      // C1 (NEL) dropped, not byte-mangled
		"中文 x":                "中文 x",     // multi-byte runes preserved
		"\x7fdel":             "del",      // DEL dropped
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
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

// captureOut swaps cliout.Out for a buffer for the duration of the test and
// returns what was written. Forces the Ascii color profile first so style.*
// never injects its own escapes — then any ESC/BEL left in the output can
// only have come from an unsanitized backend string.
func captureOut(t *testing.T, fn func()) string {
	t.Helper()
	styletest.WithColorProfile(t, termenv.Ascii)
	var buf strings.Builder
	prev := cliout.Out
	cliout.Out = &buf
	t.Cleanup(func() { cliout.Out = prev })
	fn()
	return buf.String()
}

// assertNoTerminalEscapes fails if the rendered admin output still carries
// raw terminal control bytes — the injection the sanitize() wiring exists to
// stop. (ESC drives CSI/OSC; BEL terminates an OSC title/clipboard write.)
func assertNoTerminalEscapes(t *testing.T, label, out string) {
	t.Helper()
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("%s: output contains a raw ESC (0x1b) — backend string not sanitized: %q", label, out)
	}
	if strings.ContainsRune(out, 0x07) {
		t.Errorf("%s: output contains a raw BEL (0x07) — backend string not sanitized: %q", label, out)
	}
}

// printUserRows / printUserDetail must sanitize the user-controlled backend
// fields (username/display_name are settable by any user via PUT
// /api/user/self) before they reach the admin's terminal, so `admin user
// list/search/show` can't be turned into a terminal-escape injection.
func TestPrintUserSanitizesEscapes(t *testing.T) {
	i18n.SetLanguage("en")
	u := api.AdminUserRow{
		ID:          7,
		Username:    "ev\x1b[31mil",     // CSI color
		Email:       "a\x1b]0;pwn\x07b", // OSC title set + BEL
		Role:        1,
		Status:      1,
		Group:       "grp\x1b[2J",  // CSI erase-screen
		DisplayName: "disp\x07lay", // BEL
	}

	rows := captureOut(t, func() { printUserRows([]api.AdminUserRow{u}, 0, false) })
	assertNoTerminalEscapes(t, "printUserRows", rows)
	if !strings.Contains(rows, "evil") { // sanitized visible text survives
		t.Errorf("printUserRows dropped the username text entirely: %q", rows)
	}

	detail := captureOut(t, func() { printUserDetail(&u, 0) })
	assertNoTerminalEscapes(t, "printUserDetail", detail)
	if !strings.Contains(detail, "display") {
		t.Errorf("printUserDetail dropped the display name text entirely: %q", detail)
	}
}
