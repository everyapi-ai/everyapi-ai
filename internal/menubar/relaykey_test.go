package menubar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// stubWriteClipboard swaps the package writeClipboard var with a
// recorder. The pointer returned dereferences to the most recent
// value written.
func stubWriteClipboard(t *testing.T) (recorded *string, calls *atomic.Int32) {
	t.Helper()
	prev := writeClipboard
	var captured string
	var ncalls atomic.Int32
	writeClipboard = func(s string) error {
		captured = s
		ncalls.Add(1)
		return nil
	}
	t.Cleanup(func() { writeClipboard = prev })
	return &captured, &ncalls
}

func TestRelayKeyPrefix(t *testing.T) {
	tests := []struct{ in, want string }{
		{"sk-everyapi-abcdef1234567890", "sk-everyapi-abcd…"},
		{"sk-short", "sk-short"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := relayKeyPrefix(tc.in); got != tc.want {
			t.Errorf("relayKeyPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHandleCopyRelayKey_Cached: when creds.RelayKey is populated,
// the handler writes the cached key directly without an API round-
// trip and fires the prefix notification.
func TestHandleCopyRelayKey_Cached(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	clip, calls := stubWriteClipboard(t)
	notes := captureNotifier(t)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{
		APIBase:     "http://127.0.0.1:1", // unreachable, proving we don't hit the API
		AccessToken: "tok",
		UserID:      1,
		RelayKey:    "sk-everyapi-cachedkey-1234567890",
	}

	c.handleCopyRelayKey()

	if calls.Load() != 1 {
		t.Fatalf("writeClipboard calls = %d, want 1", calls.Load())
	}
	if *clip != c.creds.RelayKey {
		t.Errorf("clipboard = %q, want %q", *clip, c.creds.RelayKey)
	}
	if len(*notes) == 0 || !strings.Contains((*notes)[0].body, "sk-everyapi-cach") {
		t.Errorf("expected prefix notification, got %+v", *notes)
	}
}

// TestHandleCopyRelayKey_Resolve: empty cache forces a /api/token/
// list + /api/token/:id fetch.
func TestHandleCopyRelayKey_Resolve(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	clip, _ := stubWriteClipboard(t)
	notes := captureNotifier(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token/":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"items": []map[string]interface{}{
						{"id": 11, "name": "newest", "status": 1, "group": ""},
					},
				},
			})
		case "/api/token/11/key":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data":    map[string]interface{}{"key": "sk-everyapi-fetched-9999"},
			})
		default:
			http.Error(w, "not found: "+r.URL.Path, 404)
		}
	}))
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}

	c.handleCopyRelayKey()

	if *clip != "sk-everyapi-fetched-9999" {
		t.Errorf("clipboard = %q", *clip)
	}
	if c.creds.RelayKey != "sk-everyapi-fetched-9999" {
		t.Errorf("creds.RelayKey not cached: %q", c.creds.RelayKey)
	}
	if len(*notes) == 0 {
		t.Error("expected confirmation notification")
	}
}

// TestHandleCopyRelayKey_NoEnabledKey returns a friendly "create
// one in dashboard" notification when the account has zero
// enabled tokens.
func TestHandleCopyRelayKey_NoEnabledKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubWriteClipboard(t)
	notes := captureNotifier(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"items": []map[string]interface{}{}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}

	c.handleCopyRelayKey()

	if len(*notes) == 0 || !strings.Contains((*notes)[0].title, "no relay") {
		t.Errorf("expected 'no relay key' notification, got %+v", *notes)
	}
}
