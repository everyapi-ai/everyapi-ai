package api

import "testing"

// TestWebOriginFromBase locks in the api→app subdomain rewrite. Two
// behaviours that have bitten us in the past, both covered explicitly:
//
//   - the http:// branch must rewrite too — before the consolidation
//     in #246, cmd/status.go silently passed http://api.* through
//     unchanged while the MCP server rewrote it, so a self-host setup
//     printed the wrong /wallet URL only in `everyapi status`;
//   - only "api." as a leading subdomain matches — bases that happen
//     to start with "api" (like https://api2.example.com or
//     https://apiserver.example.com) MUST pass through, otherwise the
//     rewrite produces nonsense hosts.
func TestWebOriginFromBase(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"https api subdomain rewrites", "https://api.everyapi.ai", "https://app.everyapi.ai"},
		{"http api subdomain rewrites (self-host over plain http)", "http://api.example.com", "http://app.example.com"},
		{"path and port preserved on rewrite", "https://api.everyapi.ai:8443", "https://app.everyapi.ai:8443"},
		{"marketing apex passes through", "https://everyapi.ai", "https://everyapi.ai"},
		{"localhost passes through", "http://localhost:8787", "http://localhost:8787"},
		{"127.0.0.1 passes through", "http://127.0.0.1:8787", "http://127.0.0.1:8787"},
		{"custom self-host without api. subdomain passes through", "https://gateway.internal.example.com", "https://gateway.internal.example.com"},
		{"api2. is NOT api. — must not be rewritten", "https://api2.example.com", "https://api2.example.com"},
		{"apiserver. is NOT api. — must not be rewritten", "https://apiserver.example.com", "https://apiserver.example.com"},
		{"empty input passes through", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WebOriginFromBase(c.in); got != c.want {
				t.Errorf("WebOriginFromBase(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
