package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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

func TestNameCell_BoldAfterPadding(t *testing.T) {
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	// Plain profile: exact width, no escapes — alignment preserved.
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := nameCell("login", 8); got != "login   " {
		t.Fatalf("plain: want %q, got %q", "login   ", got)
	}

	// Styled profile: trailing pad stays plain spaces (alignment math
	// never sees ANSI); the name carries the bold SGR.
	lipgloss.SetColorProfile(termenv.TrueColor)
	got := nameCell("login", 8)
	if !strings.HasSuffix(got, "   ") {
		t.Fatalf("styled: want 3 trailing spaces, got %q", got)
	}
	if !strings.Contains(got, "\x1b[1m") {
		t.Fatalf("styled: want bold name, got %q", got)
	}
}

// TestSessionRejected verifies the launcher entry probe: only a
// definitive HTTP 401 from /api/user/self counts as "logged out".
// A 5xx or any non-401 outcome must return false so a transient
// backend hiccup can't wall the user behind a login screen, and
// legacy credentials without a user_id skip the probe entirely.
func TestSessionRejected(t *testing.T) {
	cases := []struct {
		name    string
		userID  int
		handler http.HandlerFunc
		want    bool
	}{
		{
			name:   "401 → rejected",
			userID: 1,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"Unauthorized, invalid access token"}`))
			},
			want: true,
		},
		{
			name:   "200 → not rejected",
			userID: 1,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"u"}}`))
			},
			want: false,
		},
		{
			name:   "200 but success:false → not rejected (200 is not a 401)",
			userID: 1,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"success":false,"message":"something else"}`))
			},
			want: false,
		},
		{
			name:   "500 → not rejected (couldn't verify is not logged out)",
			userID: 1,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			want: false,
		},
		{
			name:   "legacy creds without user_id → probe skipped",
			userID: 0,
			handler: func(_ http.ResponseWriter, _ *http.Request) {
				t.Error("probe must not run for pre-user_id credentials")
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			got := sessionRejected(&config.Credentials{
				APIBase:     srv.URL,
				AccessToken: "tok",
				UserID:      tc.userID,
			})
			if got != tc.want {
				t.Errorf("sessionRejected = %v, want %v", got, tc.want)
			}
		})
	}

	// A transport failure (here: a closed server → connection
	// refused) is the design's core promise — "couldn't verify" must
	// NOT read as "logged out", or a network blip walls the user
	// behind a login that also needs the network. Same classification
	// path as a timeout: a non-*APIError error → IsUnauthorized false.
	t.Run("connection refused → not rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(
			func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close() // close before the probe so the dial fails
		got := sessionRejected(&config.Credentials{
			APIBase:     url,
			AccessToken: "tok",
			UserID:      1,
		})
		if got {
			t.Error("sessionRejected = true on a transport failure, want false")
		}
	})
}
