package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/styletest"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/muesli/termenv"
)

func TestStatusMachineEmitsSecretFreeLocalSessionJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const (
		accessToken  = "management-status-secret"
		relayKey     = "sk-everyapi-status-secret"
		refreshToken = "refresh-status-secret"
	)
	expiresAt := time.Now().Add(48 * time.Hour).Unix()
	if err := config.Save(&config.Credentials{
		APIBase:           "https://self-hosted.example",
		AccessToken:       accessToken,
		RelayKey:          relayKey,
		RefreshToken:      refreshToken,
		RelayKeyExpiresAt: expiresAt,
		Username:          "alice",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	if err := Status([]string{"--format=json"}); err != nil {
		t.Fatalf("Status machine: %v", err)
	}

	var got struct {
		Version   int    `json:"version"`
		SignedIn  bool   `json:"signed_in"`
		Username  string `json:"username"`
		APIBase   string `json:"api_base"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("machine status is not JSON: %q: %v", out.String(), err)
	}
	if got.Version != 1 || !got.SignedIn || got.Username != "alice" ||
		got.APIBase != "https://self-hosted.example" || got.ExpiresAt == "" {
		t.Fatalf("machine status = %+v", got)
	}
	for _, secret := range []string{accessToken, relayKey, refreshToken} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("machine status leaked %q: %s", secret, out.String())
		}
	}
}

func TestStatusMachineReportsSignedOutWithoutError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })

	if err := Status([]string{"--format", "json"}); err != nil {
		t.Fatalf("Status signed out: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != float64(1) || got["signed_in"] != false {
		t.Fatalf("signed-out status = %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("signed-out status exposed unexpected fields: %v", got)
	}
}

func TestStatusMachineIncludesLiveBalanceOnlyWhenRequested(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const accessToken = "management-balance-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"quota_per_unit": 100.0},
			})
		case "/api/user/self":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"username":   "alice",
					"quota":      2500,
					"used_quota": 900,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	if err := config.Save(&config.Credentials{
		APIBase:     server.URL,
		AccessToken: accessToken,
		Username:    "alice",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	if err := Status([]string{"--format=json", "--include-balance"}); err != nil {
		t.Fatalf("Status machine balance: %v", err)
	}

	var got struct {
		BalanceUSD *float64 `json:"balance_usd"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.BalanceUSD == nil || *got.BalanceUSD != 25 {
		t.Fatalf("balance_usd = %v, want 25", got.BalanceUSD)
	}
	if strings.Contains(out.String(), accessToken) {
		t.Fatalf("machine balance leaked access token: %s", out.String())
	}
}

func TestStatusMachineIncludesOAuthAccountBalance(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const relayKey = "sk-everyapi-oauth-balance-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"quota_per_unit": 100.0},
			})
		case "/api/usage/account":
			if got := r.Header.Get("Authorization"); got != "Bearer "+relayKey {
				t.Fatalf("Authorization = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"username": "alice",
					"wallet":   map[string]any{"quota": 4275, "currency": "USD"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	if err := config.Save(&config.Credentials{
		APIBase:           server.URL,
		AccessToken:       relayKey,
		RelayKey:          relayKey,
		OAuthClientID:     "everyapi-cli",
		RelayKeyExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	if err := Status([]string{"--format=json", "--include-balance"}); err != nil {
		t.Fatalf("Status OAuth machine balance: %v", err)
	}

	var got struct {
		BalanceUSD *float64 `json:"balance_usd"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.BalanceUSD == nil || *got.BalanceUSD != 42.75 {
		t.Fatalf("balance_usd = %v, want 42.75", got.BalanceUSD)
	}
	if strings.Contains(out.String(), relayKey) {
		t.Fatalf("machine balance leaked relay key: %s", out.String())
	}
}

// TestStatusLazyMigratesRole covers the pre-Role credentials.json
// recovery path: a credentials file written before the Role field
// existed has Role=0, but the live GetSelf returns 100. Status
// must rewrite the on-disk creds with the fresh Role so the help-
// gating in main.go sees the user as admin on the next invocation.
//
// XDG_CONFIG_HOME is redirected to a temp dir so the test never
// touches the developer's real ~/.config/everyapi.
func TestStatusLazyMigratesRole(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	// Fake the backend: /api/option (used by GetStatus's QuotaPerUnit
	// probe) returns a tiny envelope; /api/user/self returns a role
	// the on-disk creds doesn't yet have.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"quota_per_unit": 500000.0},
			})
		case "/api/user/self":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"id":            42,
					"username":      "xiaomo",
					"email":         "x@example.com",
					"quota":         1000,
					"used_quota":    0,
					"request_count": 0,
					"role":          100, // root
				},
			})
		default:
			// Other endpoints (jump-session, relaykey resolve) we
			// don't care about for the lazy-migrate path — return
			// a minimal success envelope so Status doesn't blow up.
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		}
	}))
	defer srv.Close()

	// Seed an OLD-format creds file: Role intentionally absent.
	if err := config.Save(&config.Credentials{
		APIBase:     srv.URL,
		AccessToken: "tok",
		UserID:      42,
		Username:    "xiaomo",
		// Role: 0 by zero-value — the very case lazy-migrate fixes.
	}); err != nil {
		t.Fatal(err)
	}
	if err := Status(nil); err != nil {
		// Status's relay-probe path may error on the stub backend
		// (it depends on resolveRelayKey returning ErrNoRelayKey
		// which prints a benign warning, not an error). Either way,
		// the role-migrate write happens before any relay step.
		t.Logf("Status returned %v (acceptable if non-relay paths still completed)", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("post-status Load: %v", err)
	}
	if got.Role != 100 {
		t.Errorf("creds.Role after status = %d, want 100 (lazy migration didn't persist)", got.Role)
	}

	// File should still be 0600 — the rewrite must preserve perms.
	// Windows has no POSIX permission bits (Stat reports 666 for any
	// writable file), so the assertion only means something elsewhere.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(tmp, "everyapi", "credentials.json"))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("credentials.json perm after rewrite = %o, want 0600", perm)
		}
	}
}

// styledQuota must bold both dollar amounts on a styled terminal and
// strip the ** markers to clean plain text when output is piped /
// NO_COLOR, so `everyapi status | grep` stays parseable.
func TestStyledQuota(t *testing.T) {
	orig := i18n.Language()
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage(orig) })

	t.Run("styled terminal bolds both amounts", func(t *testing.T) {
		styletest.WithColorProfile(t, termenv.TrueColor)
		got := styledQuota(100, 0)
		for _, want := range []string{"\x1b[1m$100.00\x1b[22m", "\x1b[1m$0.00\x1b[22m"} {
			if !strings.Contains(got, want) {
				t.Errorf("styledQuota(100, 0) = %q, missing bold span %q", got, want)
			}
		}
		if strings.Contains(got, "**") {
			t.Errorf("styledQuota(100, 0) leaked literal markers: %q", got)
		}
	})

	t.Run("piped output is plain text", func(t *testing.T) {
		styletest.WithColorProfile(t, termenv.Ascii)
		got := styledQuota(100, 0)
		want := "$100.00 remaining   $0.00 used"
		if got != want {
			t.Errorf("styledQuota(100, 0) = %q, want %q", got, want)
		}
	})
}

// TestStatusBoldsValues drives the full Status() render against a fake
// backend and asserts the value-emphasis contract: on a styled terminal
// the username, both amounts, the request count, and the topup URL are
// each wrapped in a bold span; when the profile is Ascii (piped /
// NO_COLOR) the same output carries no escape codes and no stray ** —
// so scripts parsing `everyapi status` are unaffected.
func TestStatusBoldsValues(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"quota_per_unit": 100.0},
			})
		case "/api/user/self":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"id":            7,
					"username":      "tony",
					"email":         "tony@example.com",
					"quota":         10000, // /100 = $100.00
					"used_quota":    0,     // /100 = $0.00
					"request_count": 7,
					"role":          1,
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		}
	}))
	defer srv.Close()

	if err := config.Save(&config.Credentials{
		APIBase:     srv.URL,
		AccessToken: "tok",
		UserID:      7,
		Username:    "tony",
	}); err != nil {
		t.Fatal(err)
	}

	origLang := i18n.Language()
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage(origLang) })

	origOut := cliout.Out
	var buf bytes.Buffer
	cliout.Out = &buf
	t.Cleanup(func() { cliout.Out = origOut })

	t.Run("styled terminal bolds the values", func(t *testing.T) {
		buf.Reset()
		styletest.WithColorProfile(t, termenv.TrueColor)
		_ = Status(nil) // relay probe may fail on the stub; top block is already written
		out := buf.String()

		wantSpans := []string{
			"\x1b[1mtony\x1b[22m",           // username
			"\x1b[1m$100.00\x1b[22m",        // remaining
			"\x1b[1m$0.00\x1b[22m",          // used
			"\x1b[1m7\x1b[22m",              // request count
			"\x1b[1mNOT CONFIGURED\x1b[22m", // relay verdict (no key on this stub)
		}
		for _, w := range wantSpans {
			if !strings.Contains(out, w) {
				t.Errorf("status output missing bold span %q\n--- output ---\n%s", w, out)
			}
		}
		// The topup URL is deliberately NOT bolded — terminals style
		// detected links themselves, and we keep emphasis off URLs.
		if strings.Contains(out, "/wallet\x1b[22m") {
			t.Errorf("topup URL should not be bolded\n--- output ---\n%s", out)
		}
	})

	t.Run("piped output is plain", func(t *testing.T) {
		buf.Reset()
		styletest.WithColorProfile(t, termenv.Ascii)
		_ = Status(nil)
		out := buf.String()

		if strings.Contains(out, "\x1b[") {
			t.Errorf("piped status output contains ANSI escapes:\n%q", out)
		}
		if strings.Contains(out, "**") {
			t.Errorf("piped status output leaked literal markers:\n%q", out)
		}
		if !strings.Contains(out, "$100.00 remaining   $0.00 used") {
			t.Errorf("piped status output missing plain quota line:\n%s", out)
		}
	})
}
