package cmd

import "testing"

func TestBuildVerificationURLWithCode(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		code string
		want string
	}{
		{
			"happy path",
			"https://app.everyapi.ai/cli/auth", "USR-789",
			"https://app.everyapi.ai/cli/auth?code=USR-789",
		},
		{
			"existing query preserved",
			"https://app.everyapi.ai/cli/auth?utm=email", "USR-789",
			"https://app.everyapi.ai/cli/auth?code=USR-789&utm=email",
		},
		{
			"localhost dev",
			"http://localhost:5173/cli/auth", "ABC-123",
			"http://localhost:5173/cli/auth?code=ABC-123",
		},
		{
			"empty code returns bare uri",
			"https://app.everyapi.ai/cli/auth", "",
			"https://app.everyapi.ai/cli/auth",
		},
		{
			"empty uri returns empty",
			"", "USR-789",
			"",
		},
		// A `?code=` already on the URL would be the server's choice; we Set rather than Add so we don't end up with two of them. Test pins that contract — without it a future Add refactor would silently double up.
		{
			"replaces existing code param",
			"https://app.everyapi.ai/cli/auth?code=OLD", "NEW",
			"https://app.everyapi.ai/cli/auth?code=NEW",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildVerificationURLWithCode(c.uri, c.code)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestIsDisplayableURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"https ok", "https://app.everyapi.ai/cli/auth?code=X", true},
		{"http localhost ok", "http://localhost:5173/cli/auth", true},
		{"empty", "", false},
		{"leading dash (option injection)", "-a", false},
		{"leading dash url", "-https://evil", false},
		{"embedded ESC (OSC 8 spoof)", "https://app.everyapi.ai\x1b]8;;https://evil\x07x", false},
		{"embedded newline", "https://app.everyapi.ai\nfoo", false},
		{"DEL byte", "https://app.everyapi.ai\x7f", false},
		{"non-http scheme", "file:///etc/passwd", false},
		{"javascript scheme", "javascript:alert(1)", false},
		{"relative (no host)", "/cli/auth?code=X", false},
		{"scheme without host", "https://", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDisplayableURL(c.in); got != c.want {
				t.Errorf("isDisplayableURL(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
