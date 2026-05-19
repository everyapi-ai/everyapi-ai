package menubar

import "testing"

func TestFormatUSD(t *testing.T) {
	tests := []struct {
		name    string
		quota   int64
		perUnit float64
		want    string
	}{
		{"zero", 0, 500000, "$0.00"},
		{"typical", 5000000, 500000, "$10.00"},
		{"fractional", 1234567, 500000, "$2.47"},
		{"per-unit-zero falls back to int", 12345, 0, "12345"},
		{"negative perUnit also falls back", 12345, -1, "12345"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatUSD(tc.quota, tc.perUnit)
			if got != tc.want {
				t.Errorf("formatUSD(%d, %v) = %q, want %q", tc.quota, tc.perUnit, got, tc.want)
			}
		})
	}
}

func TestBuildVerificationURLWithCode(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		code string
		want string
	}{
		{
			name: "happy path",
			uri:  "https://everyapi.ai/cli/auth",
			code: "ABCD-1234",
			want: "https://everyapi.ai/cli/auth?code=ABCD-1234",
		},
		{
			name: "preserves existing query",
			uri:  "https://everyapi.ai/cli/auth?utm=launch",
			code: "ABCD-1234",
			want: "https://everyapi.ai/cli/auth?code=ABCD-1234&utm=launch",
		},
		{
			name: "empty code returns uri unchanged",
			uri:  "https://everyapi.ai/cli/auth",
			code: "",
			want: "https://everyapi.ai/cli/auth",
		},
		{
			name: "empty uri returns empty",
			uri:  "",
			code: "ABCD-1234",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildVerificationURLWithCode(tc.uri, tc.code)
			if got != tc.want {
				t.Errorf("buildVerificationURLWithCode(%q, %q) = %q, want %q",
					tc.uri, tc.code, got, tc.want)
			}
		})
	}
}

func TestFormatInt(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1,000"},
		{12345, "12,345"},
		{1234567, "1,234,567"},
		{-1234, "-1,234"},
	}
	for _, tc := range tests {
		if got := formatInt(tc.in); got != tc.want {
			t.Errorf("formatInt(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
