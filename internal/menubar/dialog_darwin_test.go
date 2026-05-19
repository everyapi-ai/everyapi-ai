//go:build darwin

package menubar

import "testing"

func TestOsaEscape(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"plain ascii", "plain ascii"},
		{`with "quotes"`, `with \"quotes\"`},
		{`with \backslash`, `with \\backslash`},
		{`both " and \`, `both \" and \\`},
		{"emoji 🐶🌟🎵🔵", "emoji 🐶🌟🎵🔵"},
		{"", ""},
		// R3 newline / control char handling — earlier osaEscape
		// would leave the literal \n inside the AppleScript double-
		// quoted string, terminating the string and producing a
		// silent dialog-doesn't-appear failure.
		{"line1\nline2", `line1" & return & "line2`},
		{"line1\r\nline2", `line1" & return & "" & return & "line2`},
		{"col1\tcol2", `col1" & tab & "col2`},
		{"trailing-nul\x00rest", "trailing-nulrest"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := osaEscape(tc.in); got != tc.want {
				t.Errorf("osaEscape(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
