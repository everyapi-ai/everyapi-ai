package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// tokenListAndKeyServer is a small fake backend that serves
// /api/token/ (list) and /api/token/{id}/key (key fetch) with
// caller-supplied items + keys. Saves test boilerplate.
func tokenListAndKeyServer(t *testing.T, items []map[string]interface{}, keys map[int]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token/":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data":    map[string]interface{}{"items": items},
			})
		default:
			// Match /api/token/<id>/key
			for id, key := range keys {
				if r.URL.Path == "/api/token/"+itoaInt(id)+"/key" {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"success": true,
						"data":    map[string]interface{}{"key": key},
					})
					return
				}
			}
			http.Error(w, "not found: "+r.URL.Path, 404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func itoaInt(n int) string {
	// strconv import is overkill for one call site; tiny inline.
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	neg := n < 0
	if neg {
		n = -n
	}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestResolveRelayKey_CacheHit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Unreachable API on purpose — cache hit must NOT phone home.
	creds := &config.Credentials{
		APIBase:     "http://127.0.0.1:1",
		AccessToken: "tok",
		UserID:      1,
		RelayKey:    "sk-everyapi-cached-xxx",
	}
	got, err := ResolveRelayKey(context.Background(), creds, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "sk-everyapi-cached-xxx" {
		t.Errorf("key = %q, want cached", got)
	}
}

func TestResolveRelayKey_DefaultGroupSaveBack(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := tokenListAndKeyServer(t,
		[]map[string]interface{}{
			{"id": 11, "name": "newest", "status": TokenStatusEnabled, "group": ""},
		},
		map[int]string{11: "sk-everyapi-fresh-1234"},
	)
	creds := &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	got, err := ResolveRelayKey(context.Background(), creds, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "sk-everyapi-fresh-1234" {
		t.Errorf("key = %q", got)
	}
	if creds.RelayKey != "sk-everyapi-fresh-1234" {
		t.Errorf("creds.RelayKey not cached: %q", creds.RelayKey)
	}
	// Verify the save-back actually hit disk.
	cfgDir, _ := config.ConfigDir()
	if _, err := os.Stat(filepath.Join(cfgDir, "credentials.json")); err != nil {
		t.Errorf("credentials.json not written: %v", err)
	}
}

func TestResolveRelayKey_GroupBypass(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := tokenListAndKeyServer(t,
		[]map[string]interface{}{
			{"id": 7, "name": "default", "status": TokenStatusEnabled, "group": ""},
			{"id": 9, "name": "prod-only", "status": TokenStatusEnabled, "group": "prod"},
		},
		map[int]string{
			7: "sk-everyapi-default-key",
			9: "sk-everyapi-prod-key",
		},
	)
	// Pre-cache the default-group key; the group="prod" lookup must
	// NOT see it, NOT save back the prod key on top of it.
	creds := &config.Credentials{
		APIBase:     srv.URL,
		AccessToken: "tok",
		UserID:      1,
		RelayKey:    "sk-everyapi-prior-cache",
	}
	got, err := ResolveRelayKey(context.Background(), creds, "prod")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "sk-everyapi-prod-key" {
		t.Errorf("key = %q, want prod", got)
	}
	if creds.RelayKey != "sk-everyapi-prior-cache" {
		t.Errorf("group bypass leaked into default-group cache: %q", creds.RelayKey)
	}
}

func TestResolveRelayKey_NoEnabledKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := tokenListAndKeyServer(t, []map[string]interface{}{}, nil)
	creds := &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}

	_, err := ResolveRelayKey(context.Background(), creds, "")
	if !errors.Is(err, ErrNoRelayKey) {
		t.Errorf("err = %v, want ErrNoRelayKey", err)
	}
}

func TestResolveRelayKey_NoKeyInGroup(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := tokenListAndKeyServer(t,
		[]map[string]interface{}{
			{"id": 1, "name": "default", "status": TokenStatusEnabled, "group": ""},
		},
		map[int]string{1: "sk-everyapi-default"},
	)
	creds := &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}

	_, err := ResolveRelayKey(context.Background(), creds, "missing-group")
	if !errors.Is(err, ErrNoRelayKeyForGroup) {
		t.Errorf("err = %v, want ErrNoRelayKeyForGroup", err)
	}
}

func TestResolveRelayKey_ErrCacheSaveCarriesKey(t *testing.T) {
	// Point XDG_CONFIG_HOME at a path that can't be written:
	// pre-create a regular FILE where the resolver expects a dir.
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "everyapi")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", tmp)

	srv := tokenListAndKeyServer(t,
		[]map[string]interface{}{
			{"id": 1, "name": "default", "status": TokenStatusEnabled, "group": ""},
		},
		map[int]string{1: "sk-everyapi-key"},
	)
	creds := &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}

	key, err := ResolveRelayKey(context.Background(), creds, "")
	if key != "sk-everyapi-key" {
		t.Errorf("key on cache-save failure = %q, want resolved value (so callers can still complete the action)", key)
	}
	var saveErr *ErrCacheSave
	if !errors.As(err, &saveErr) {
		t.Fatalf("err = %v, want *ErrCacheSave", err)
	}
	if saveErr.Unwrap() == nil {
		t.Error("ErrCacheSave.Unwrap returned nil — should carry the underlying mkdir/write error")
	}
}

func TestResolveRelayKey_SkipDisabledTokens(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := tokenListAndKeyServer(t,
		[]map[string]interface{}{
			{"id": 1, "name": "disabled-newest", "status": 2 /* not enabled */, "group": ""},
			{"id": 2, "name": "enabled-second", "status": TokenStatusEnabled, "group": ""},
		},
		map[int]string{2: "sk-everyapi-enabled"},
	)
	creds := &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}

	got, err := ResolveRelayKey(context.Background(), creds, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "sk-everyapi-enabled" {
		t.Errorf("key = %q, want the enabled one (skipping disabled)", got)
	}
}
