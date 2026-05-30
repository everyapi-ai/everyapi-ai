package admin

import (
	"bufio"
	"strings"
	"testing"
)

// TestConsoleAreas_Shape locks the console's structure: the seven areas
// in display order, with log/audit as leaves (single no-arg action, run
// directly) and the rest carrying a non-empty action set whose rows all
// have a collect func.
func TestConsoleAreas_Shape(t *testing.T) {
	areas := consoleAreas()
	wantOrder := []string{"marketplace", "user", "channel", "log", "abuse", "audit", "redemption"}
	if len(areas) != len(wantOrder) {
		t.Fatalf("got %d areas, want %d", len(areas), len(wantOrder))
	}
	for i, a := range areas {
		if a.name != wantOrder[i] {
			t.Errorf("area[%d] = %q, want %q", i, a.name, wantOrder[i])
		}
		leaf := a.name == "log" || a.name == "audit"
		if leaf {
			if a.actions != nil || len(a.leafArgs) == 0 {
				t.Errorf("%s should be a leaf (leafArgs set, no actions)", a.name)
			}
			continue
		}
		if len(a.actions) == 0 {
			t.Errorf("%s should have actions", a.name)
		}
		for _, act := range a.actions {
			if act.collect == nil {
				t.Errorf("%s/%s has no collect func", a.name, act.verb)
			}
		}
	}
}

// TestConsoleCollect_Args drives the text-prompt collect funcs with canned
// stdin and asserts the assembled argv — the contract that what the picker
// gathers is exactly what `everyapi admin …` would receive. Choice-based
// actions (manage/tag/status) read os.Stdin via cliprompt.Pick and are
// covered by TestConsoleAreas_Shape's wiring check instead.
func TestConsoleCollect_Args(t *testing.T) {
	rdr := func(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }
	cases := []struct {
		name    string
		collect func(*bufio.Reader) ([]string, error)
		input   string
		want    []string
	}{
		{"marketplace on", noArgs("marketplace", "on"), "", []string{"marketplace", "on"}},
		{"user show", collectID("user", "show"), "42\n", []string{"user", "show", "42"}},
		{"user delete", collectID("user", "delete"), "7\n", []string{"user", "delete", "7"}},
		{"user search", collectSearch("user"), "alice\n", []string{"user", "search", "alice"}},
		{"abuse update no note", collectAbuseUpdate, "5\nresolved\n\n", []string{"abuse", "update", "5", "--status", "resolved"}},
		{"abuse update with note", collectAbuseUpdate, "5\nresolved\nspam ring\n", []string{"abuse", "update", "5", "--status", "resolved", "--note", "spam ring"}},
		{"redemption create minimal", collectRedemptionCreate, "promo\n1000\n\n\n", []string{"redemption", "create", "--name", "promo", "--quota", "1000"}},
		{"redemption create full", collectRedemptionCreate, "promo\n1000\n10\n0\n", []string{"redemption", "create", "--name", "promo", "--quota", "1000", "--count", "10", "--expires", "0"}},
		{"redemption update partial", collectRedemptionUpdate, "3\n\n500\n\n", []string{"redemption", "update", "3", "--quota", "500"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.collect(rdr(tc.input))
			if err != nil {
				t.Fatalf("collect error: %v", err)
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("args = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPromptID_RejectsNonPositive re-prompts past a bad value rather than
// aborting, so a typo doesn't kick the operator out of the action.
func TestPromptID_RejectsNonPositive(t *testing.T) {
	got, err := promptID(bufio.NewReader(strings.NewReader("abc\n0\n12\n")), "admin.prompt.id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "12" {
		t.Errorf("promptID = %q, want %q (should skip 'abc' and '0')", got, "12")
	}
}
