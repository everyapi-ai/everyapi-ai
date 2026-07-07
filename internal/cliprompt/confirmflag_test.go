package cliprompt

import (
	"reflect"
	"testing"
)

// TestSplitConfirmFlag pins the accepted confirm-skip spellings and the
// any-order positional extraction. The accepted set is the union of what
// the previous per-command flag.Bool("y")/flag.Bool("yes") registrations
// matched — crucially including the single-dash `-yes` form, whose loss
// would silently change `token revoke 5 -yes` from confirm-skip to a
// fail-closed error in non-interactive use.
func TestSplitConfirmFlag(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		skip    bool
		posWant []string
	}{
		{"none", []string{"5"}, false, []string{"5"}},
		{"y-first", []string{"-y", "5"}, true, []string{"5"}},
		{"y-last", []string{"5", "-y"}, true, []string{"5"}},
		{"double-y", []string{"--y", "5"}, true, []string{"5"}},
		{"single-dash-yes", []string{"5", "-yes"}, true, []string{"5"}},
		{"double-dash-yes", []string{"--yes", "5"}, true, []string{"5"}},
		{"empty", nil, false, nil},
		{"only-flag", []string{"-y"}, true, nil},
		{"non-confirm-flag-kept", []string{"-x", "5"}, false, []string{"-x", "5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skip, pos := SplitConfirmFlag(tc.args)
			if skip != tc.skip {
				t.Errorf("skip = %v, want %v", skip, tc.skip)
			}
			if !reflect.DeepEqual(pos, tc.posWant) {
				t.Errorf("positional = %v, want %v", pos, tc.posWant)
			}
		})
	}
}
