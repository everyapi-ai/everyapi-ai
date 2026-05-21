package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

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
	info, err := os.Stat(filepath.Join(tmp, "everyapi", "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials.json perm after rewrite = %o, want 0600", perm)
	}
}
