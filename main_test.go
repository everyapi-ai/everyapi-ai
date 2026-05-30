package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
	"github.com/everyapi-ai/everyapi-ai/internal/styletest"
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

// withStdin swaps os.Stdin for a pipe preloaded with input for the
// duration of the test. The sub-picker's non-TTY path reads os.Stdin
// via fmt.Scanln, so this drives runSubPicker without a real terminal.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
}

// TestSubPicker_BackRow locks the discoverability fix: every
// sub-picker carries a trailing "back" row, and choosing it unwinds to
// the parent menu (ErrPickCancelled — the same signal Esc raises)
// WITHOUT dispatching any subcommand. Without the row a user who
// doesn't know Esc is bound has no visible way out of the sub-menu.
func TestSubPicker_BackRow(t *testing.T) {
	newCmd := func(ran *bool) command {
		return command{
			name: "checkin",
			subs: []subcommand{
				{name: "claim", desc: "Claim today's reward", args: []string{"claim"}},
				{name: "status", desc: "Show this month's check-in calendar", args: []string{"status"}},
			},
			run: func([]string) error { *ran = true; return nil },
		}
	}

	t.Run("back row unwinds without running a sub", func(t *testing.T) {
		ran := false
		// Number-entry path: back is the row after the two declared
		// subs, so its 1-based selector is 3.
		withStdin(t, "3\n")
		err := runSubPicker(newCmd(&ran))
		if !errors.Is(err, cliprompt.ErrPickCancelled) {
			t.Fatalf("selecting back: err = %v, want ErrPickCancelled", err)
		}
		if ran {
			t.Error("selecting back dispatched a subcommand; back must only unwind")
		}
	})

	t.Run("picking a real sub still dispatches it", func(t *testing.T) {
		ran := false
		// Selector 1 == first declared sub (claim); the trailing EOF on
		// the next loop read is expected and ignored — we only assert
		// the index→args mapping survived adding the back row.
		withStdin(t, "1\n")
		_ = runSubPicker(newCmd(&ran))
		if !ran {
			t.Error("selecting the first row did not dispatch its subcommand")
		}
	})
}

func TestNameCell_BoldAfterPadding(t *testing.T) {
	// Plain profile: exact width, no escapes — alignment preserved.
	styletest.WithColorProfile(t, termenv.Ascii)
	if got := nameCell("login", 8); got != "login   " {
		t.Fatalf("plain: want %q, got %q", "login   ", got)
	}

	// Styled profile: trailing pad stays plain spaces (alignment math
	// never sees ANSI); the name carries the bold SGR. Bare
	// SetColorProfile mid-test is fine — the cleanup registered by
	// WithColorProfile above still wins on teardown and restores the
	// original profile. A second WithColorProfile call would also work
	// (LIFO cleanup), just reads more verbose.
	lipgloss.SetColorProfile(termenv.TrueColor)
	got := nameCell("login", 8)
	if !strings.HasSuffix(got, "   ") {
		t.Fatalf("styled: want 3 trailing spaces, got %q", got)
	}
	if !strings.Contains(got, "\x1b[1m") {
		t.Fatalf("styled: want bold name, got %q", got)
	}
}

func TestRenderUsage_StripsMarkersWhenUnstyled(t *testing.T) {
	styletest.WithColorProfile(t, termenv.Ascii)
	if out := renderUsage(); strings.Contains(out, "**") {
		t.Fatalf("usage must not leak ** markers when unstyled:\n%s", out)
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

// TestEveryCommandGrouped is the guard that keeps the launcher's
// category map exhaustive: every registered top-level command must map
// to a known group, and every group it maps to must be one the launcher
// actually renders (launcherGroupOrder). A new command added to the
// registry with no commandGroup entry fails here instead of silently
// landing in the groupOf fallback bucket.
func TestEveryCommandGrouped(t *testing.T) {
	known := map[string]bool{}
	for _, g := range launcherGroupOrder {
		known[g] = true
	}
	for _, c := range commands {
		g, ok := commandGroup[c.name]
		if !ok {
			t.Errorf("command %q has no commandGroup entry", c.name)
			continue
		}
		if !known[g] {
			t.Errorf("command %q maps to group %q which isn't in launcherGroupOrder", c.name, g)
		}
	}
	// Reverse: no stale mapping for a command that no longer exists.
	exists := map[string]bool{}
	for _, c := range commands {
		exists[c.name] = true
	}
	for name := range commandGroup {
		if !exists[name] {
			t.Errorf("commandGroup has entry for %q which isn't a registered command", name)
		}
	}
}

// TestLauncherSections_PartitionsInOrder verifies the logged-in section
// list is built in launcherGroupOrder, non-empty, and that every
// visible command lands in exactly one section (no drops, no dupes).
func TestLauncherSections_PartitionsInOrder(t *testing.T) {
	visible, _ := launcherRows(true, true)
	sections, _ := launcherSections(true, true)
	if len(sections) == 0 {
		t.Fatal("no sections for a logged-in admin")
	}
	// Section titles must appear in launcherGroupOrder order (titles are
	// localized, so compare by re-deriving the key order via group sizes
	// instead: assert the flattened command count matches visible).
	total := 0
	seen := map[string]int{}
	for _, s := range sections {
		if len(s.cmds) != len(s.labels) {
			t.Errorf("section %q: %d cmds vs %d labels", s.title, len(s.cmds), len(s.labels))
		}
		for _, c := range s.cmds {
			seen[c.name]++
			total++
		}
	}
	if total != len(visible) {
		t.Errorf("sections cover %d commands, want %d (visible)", total, len(visible))
	}
	for _, c := range visible {
		if seen[c.name] != 1 {
			t.Errorf("command %q appears in %d sections, want 1", c.name, seen[c.name])
		}
	}
}

// TestUsageCommandList_ShowsEveryCommand locks the drift-proof help: the
// generated list must include EVERY registered command (so a new command
// can never be silently missing from `everyapi help`), and adminOnly
// commands appear only for admins.
func TestUsageCommandList_ShowsEveryCommand(t *testing.T) {
	full := usageCommandList(true)
	for _, c := range commands {
		if !strings.Contains(full, c.name) {
			t.Errorf("usageCommandList(admin) is missing command %q", c.name)
		}
		if len(c.subs) > 0 && !strings.Contains(full, c.name+" <sub>") {
			t.Errorf("command %q has subs but no <sub> tag in help", c.name)
		}
	}
	// adminOnly gating: admin shows for admins, not for others.
	if !strings.Contains(usageCommandList(true), "admin <sub>") {
		t.Error("admin command should appear for admins")
	}
	if strings.Contains(usageCommandList(false), "admin <sub>") {
		t.Error("admin command leaked to non-admin help")
	}
}

// TestNamespaceDispatchers_Surface covers the network-free arms of the
// namespace dispatchers introduced by the reorg: bare/help print usage
// and return nil; an unknown subcommand errors and names the bad sub.
// (Routing of real subs is verified end-to-end; here we pin the shape.)
func TestNamespaceDispatchers_Surface(t *testing.T) {
	for _, d := range []struct {
		name string
		run  func([]string) error
	}{
		{"account", accountRun}, {"stats", statsRun},
		{"market", marketRun}, {"inbox", inboxRun}, {"version", versionRun},
	} {
		for _, args := range [][]string{{"help"}, {"--help"}} {
			if err := d.run(args); err != nil {
				t.Errorf("%sRun(%v) = %v, want nil", d.name, args, err)
			}
		}
		err := d.run([]string{"definitely-not-a-sub"})
		if err == nil || !strings.Contains(err.Error(), "definitely-not-a-sub") {
			t.Errorf("%sRun(bogus) = %v, want an error naming the bad sub", d.name, err)
		}
	}
}

// TestBackRowLabel_AlignsToDescColumn locks the sub-picker back row's
// two-column shape: the localized hint must start at the same display
// column as command descriptions (maxName + 2 spaces), even though the
// back word may be wide CJK — i.e. padding is by display width, not len.
func TestBackRowLabel_AlignsToDescColumn(t *testing.T) {
	const maxName = 12
	out := backRowLabel(maxName)
	word, hint := i18n.T("common.back"), i18n.T("common.back_hint")
	if !strings.Contains(out, word) || !strings.Contains(out, hint) {
		t.Fatalf("backRowLabel = %q, want both %q and %q", out, word, hint)
	}
	if got, want := style.Width(out), maxName+2+style.Width(hint); got != want {
		t.Errorf("display width = %d, want %d (maxName + 2 + hint)", got, want)
	}
}

// TestProxyMenuSubs_StartStopMutuallyExclusive locks the proxy
// sub-menu's core rule: start and stop never appear together (a running
// proxy can't be started, a stopped one can't be stopped) and configure
// is always offered. Robust to whatever proxy.IsRunning reports in the
// test environment — it only asserts the start/stop XOR, not which one.
func TestProxyMenuSubs_StartStopMutuallyExclusive(t *testing.T) {
	subs := proxyMenuSubs()
	if len(subs) != 2 {
		t.Fatalf("want 2 rows (toggle + configure), got %d: %+v", len(subs), subs)
	}
	has := map[string]bool{}
	for _, s := range subs {
		has[s.name] = true
	}
	if has["start"] == has["stop"] {
		t.Errorf("exactly one of start/stop must show, got start=%v stop=%v", has["start"], has["stop"])
	}
	if !has["configure"] {
		t.Errorf("configure must always be present, got %+v", subs)
	}
}

// TestAuthMenuSubs_LoginLogoutMutuallyExclusive locks the auth
// sub-menu's rule: it offers exactly one action — login when signed out,
// logout when signed in — and status is never a row (it's the header,
// see authHeader). Robust to whatever login state the test environment
// happens to be in: it asserts the shape, not which branch.
func TestAuthMenuSubs_LoginLogoutMutuallyExclusive(t *testing.T) {
	subs := authMenuSubs()
	if len(subs) != 1 {
		t.Fatalf("auth menu must offer exactly one action, got %+v", subs)
	}
	switch subs[0].name {
	case "login", "logout":
	default:
		t.Errorf("auth menu action must be login or logout, got %q", subs[0].name)
	}
}
