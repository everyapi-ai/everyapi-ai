package cmd

import (
	"reflect"
	"testing"
)

func TestParseUseArgs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantTool   string
		wantGroup  string
		wantPick   bool
		wantDirect bool
		wantExtra  []string
		wantErr    bool
	}{
		{"bare tool", []string{"claude"}, "claude", "", false, false, nil, false},
		{"no args", nil, "", "", false, false, nil, false},
		{"tool then bare channel → picker", []string{"claude", "--channel"}, "claude", "", true, false, nil, false},
		{"tool then bare group → picker", []string{"claude", "--group"}, "claude", "", true, false, nil, false},
		{"bare channel only → picker, no tool", []string{"--channel"}, "", "", true, false, nil, false},
		{"space value after tool", []string{"claude", "--channel", "byteplus"}, "claude", "byteplus", false, false, nil, false},
		{"space value before tool", []string{"--channel", "byteplus", "claude"}, "claude", "byteplus", false, false, nil, false},
		{"group alias space value", []string{"claude", "--group", "byteplus"}, "claude", "byteplus", false, false, nil, false},
		{"eq value", []string{"claude", "--channel=byteplus"}, "claude", "byteplus", false, false, nil, false},
		{"empty eq → picker", []string{"claude", "--channel="}, "claude", "", true, false, nil, false},
		{"single dash space value", []string{"-channel", "byteplus", "codex"}, "codex", "byteplus", false, false, nil, false},
		{"value position is known tool, no tool yet → it's the tool, picker", []string{"--channel", "claude"}, "claude", "", true, false, nil, false},
		{"next token is a flag → picker", []string{"claude", "--channel", "--direct"}, "claude", "", true, true, nil, false},
		{"direct flips direct=true", []string{"claude", "--direct"}, "claude", "", false, true, nil, false},
		{"direct + group", []string{"claude", "--direct", "--channel=byteplus"}, "claude", "byteplus", false, true, nil, false},
		{"two positionals → error", []string{"claude", "extra"}, "", "", false, false, nil, true},
		{"unknown flag → error", []string{"claude", "--bogus"}, "", "", false, false, nil, true},
		{"ambiguous: group named like a tool before tool → error", []string{"--group", "codex", "claude"}, "", "", false, false, nil, true},
		{"eq form lets a tool-named group through", []string{"--channel=codex", "claude"}, "claude", "codex", false, false, nil, false},

		// `--` separator: end of everyapi's option parsing, everything
		// after is forwarded raw to the tool. The documented escape
		// hatch for tool flags that collide with everyapi's, e.g.
		// claude's `--dangerously-skip-permissions` and codex's
		// `--dangerously-bypass-approvals-and-sandbox`.
		{"-- forwards a single flag", []string{"claude", "--", "--dangerously-skip-permissions"}, "claude", "", false, false, []string{"--dangerously-skip-permissions"}, false},
		{"-- forwards multiple tokens verbatim", []string{"claude", "--", "--model", "opus", "prompt text"}, "claude", "", false, false, []string{"--model", "opus", "prompt text"}, false},
		{"-- combined with --channel before tool", []string{"--channel", "byteplus", "claude", "--", "--dangerously-skip-permissions"}, "claude", "byteplus", false, false, []string{"--dangerously-skip-permissions"}, false},
		{"-- combined with --channel after tool", []string{"claude", "--channel=byteplus", "--", "--dangerously-skip-permissions"}, "claude", "byteplus", false, false, []string{"--dangerously-skip-permissions"}, false},
		{"bare -- with no following args", []string{"claude", "--"}, "claude", "", false, false, nil, false},
		{"-- shields what would otherwise be a everyapi flag", []string{"claude", "--", "--group", "byteplus"}, "claude", "", false, false, []string{"--group", "byteplus"}, false},
		{"-- shields what would otherwise be a second positional", []string{"claude", "--", "extra"}, "claude", "", false, false, []string{"extra"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool, group, pick, direct, extra, err := parseUseArgs(c.args)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if tool != c.wantTool || group != c.wantGroup || pick != c.wantPick || direct != c.wantDirect {
				t.Fatalf("parseUseArgs(%q) = (tool %q, group %q, pick %v, direct %v), want (%q, %q, %v, %v)",
					c.args, tool, group, pick, direct, c.wantTool, c.wantGroup, c.wantPick, c.wantDirect)
			}
			if !reflect.DeepEqual(extra, c.wantExtra) {
				t.Fatalf("parseUseArgs(%q) extra = %#v, want %#v", c.args, extra, c.wantExtra)
			}
		})
	}
}
