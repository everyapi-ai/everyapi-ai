package cmd

import (
	"reflect"
	"testing"
)

func TestParseUseArgs(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantTool  string
		wantGroup string
		wantPick  bool
		wantExtra []string
		wantErr   bool
	}{
		{"bare tool", []string{"claude"}, "claude", "", false, nil, false},
		{"no args", nil, "", "", false, nil, false},
		{"tool then bare channel → picker", []string{"claude", "--channel"}, "claude", "", true, nil, false},
		{"tool then bare group → picker", []string{"claude", "--group"}, "claude", "", true, nil, false},
		{"bare channel only → picker, no tool", []string{"--channel"}, "", "", true, nil, false},
		{"space value after tool", []string{"claude", "--channel", "byteplus"}, "claude", "byteplus", false, nil, false},
		{"space value before tool", []string{"--channel", "byteplus", "claude"}, "claude", "byteplus", false, nil, false},
		{"group alias space value", []string{"claude", "--group", "byteplus"}, "claude", "byteplus", false, nil, false},
		{"eq value", []string{"claude", "--channel=byteplus"}, "claude", "byteplus", false, nil, false},
		{"empty eq → picker", []string{"claude", "--channel="}, "claude", "", true, nil, false},
		{"single dash space value", []string{"-channel", "byteplus", "codex"}, "codex", "byteplus", false, nil, false},
		{"value position is known tool, no tool yet → it's the tool, picker", []string{"--channel", "claude"}, "claude", "", true, nil, false},
		{"next token is a flag → picker", []string{"claude", "--channel", "--direct"}, "claude", "", true, nil, false},
		{"direct ignored", []string{"claude", "--direct"}, "claude", "", false, nil, false},
		{"two positionals → error", []string{"claude", "extra"}, "", "", false, nil, true},
		{"unknown flag → error", []string{"claude", "--bogus"}, "", "", false, nil, true},
		{"ambiguous: group named like a tool before tool → error", []string{"--group", "codex", "claude"}, "", "", false, nil, true},
		{"eq form lets a tool-named group through", []string{"--channel=codex", "claude"}, "claude", "codex", false, nil, false},

		// `--` separator: end of relaya's option parsing, everything
		// after is forwarded raw to the tool. The documented escape
		// hatch for tool flags that collide with relaya's, e.g.
		// claude's `--dangerously-skip-permissions` and codex's
		// `--dangerously-bypass-approvals-and-sandbox`.
		{"-- forwards a single flag", []string{"claude", "--", "--dangerously-skip-permissions"}, "claude", "", false, []string{"--dangerously-skip-permissions"}, false},
		{"-- forwards multiple tokens verbatim", []string{"claude", "--", "--model", "opus", "prompt text"}, "claude", "", false, []string{"--model", "opus", "prompt text"}, false},
		{"-- combined with --channel before tool", []string{"--channel", "byteplus", "claude", "--", "--dangerously-skip-permissions"}, "claude", "byteplus", false, []string{"--dangerously-skip-permissions"}, false},
		{"-- combined with --channel after tool", []string{"claude", "--channel=byteplus", "--", "--dangerously-skip-permissions"}, "claude", "byteplus", false, []string{"--dangerously-skip-permissions"}, false},
		{"bare -- with no following args", []string{"claude", "--"}, "claude", "", false, nil, false},
		{"-- shields what would otherwise be a relaya flag", []string{"claude", "--", "--group", "byteplus"}, "claude", "", false, []string{"--group", "byteplus"}, false},
		{"-- shields what would otherwise be a second positional", []string{"claude", "--", "extra"}, "claude", "", false, []string{"extra"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool, group, pick, extra, err := parseUseArgs(c.args)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if tool != c.wantTool || group != c.wantGroup || pick != c.wantPick {
				t.Fatalf("parseUseArgs(%q) = (tool %q, group %q, pick %v), want (%q, %q, %v)",
					c.args, tool, group, pick, c.wantTool, c.wantGroup, c.wantPick)
			}
			if !reflect.DeepEqual(extra, c.wantExtra) {
				t.Fatalf("parseUseArgs(%q) extra = %#v, want %#v", c.args, extra, c.wantExtra)
			}
		})
	}
}
