package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

// TestRenderUsageGatedByRole verifies the help-text renderer hides
// the admin subcommand block from non-admin / unauthenticated users
// and shows it for role >= 10 (RoleAdminUser). The check is purely
// local — backend still enforces; this is a discoverability filter,
// not a security boundary.
func TestRenderUsageGatedByRole(t *testing.T) {
	cases := []struct {
		name    string
		role    int
		creds   bool
		wantAdm bool
	}{
		{name: "no credentials → no admin block", creds: false, wantAdm: false},
		{name: "common user (role=1) → no admin block", role: 1, creds: true, wantAdm: false},
		{name: "guest (role=0) → no admin block", role: 0, creds: true, wantAdm: false},
		{name: "admin (role=10) → admin block shown", role: 10, creds: true, wantAdm: true},
		{name: "root (role=100) → admin block shown", role: 100, creds: true, wantAdm: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", tmp)
			if tc.creds {
				if err := config.Save(&config.Credentials{
					APIBase:     "https://api.example.com",
					AccessToken: "tok",
					Role:        tc.role,
				}); err != nil {
					t.Fatal(err)
				}
			}
			out := renderUsage()
			gotAdm := strings.Contains(out, "admin <sub>")
			if gotAdm != tc.wantAdm {
				t.Errorf("admin block visibility = %v, want %v", gotAdm, tc.wantAdm)
			}
			// Sanity: the rest of the usage (proxy line) is always present.
			if !strings.Contains(out, "proxy <sub>") {
				t.Error("proxy block missing — renderUsage cut something it shouldn't have")
			}
			// Sanity: file location used.
			_ = filepath.Join(tmp, "everyapi", "credentials.json")
		})
	}
}
