package cliout

import "testing"

func TestSanitizeTerminalControls(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain unicode", "用户 café 🚀", "用户 café 🚀"},
		{"tabs kept and lines folded", "a\tb\r\nc", "a\tb  c"},
		{"C0 and DEL dropped", "a\x00\x08b\x7fc", "abc"},
		{"UTF-8 C1 dropped", "a\u0085b\u009fc", "abc"},
		{"invalid UTF-8 dropped", string([]byte{'a', 0xff, 'b'}), "ab"},
		{"CSI color stripped", "a\x1b[31mred\x1b[0mb", "aredb"},
		{"OSC BEL stripped", "a\x1b]52;c;secret\x07b", "ab"},
		{"OSC ST stripped", "a\x1b]8;;https://evil.invalid\x1b\\link\x1b]8;;\x1b\\b", "alinkb"},
		{"short escape stripped", "a\x1bcb", "ab"},
		{"lone escape stripped", "a\x1b", "a"},
		{"unterminated CSI stripped", "a\x1b[31", "a"},
		{"unterminated OSC stripped", "a\x1b]title", "a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in); got != tc.want {
				t.Fatalf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func FuzzSanitizeNeverEmitsTerminalControls(f *testing.F) {
	for _, seed := range []string{"plain", "\x1b[31mred", "\x1b]52;c;x\x07", "用户\nname", string([]byte{0xff, 0x9b})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		out := Sanitize(input)
		for _, r := range out {
			if r == '\x1b' || r == '\x7f' || (r < 0x20 && r != '\t') || (r >= 0x80 && r <= 0x9f) {
				t.Fatalf("Sanitize emitted terminal control U+%04X from %q", r, input)
			}
		}
	})
}
