package menubar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

func TestRunDeviceAuth_HappyPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	browser := stubOpenBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/device-auth-start", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"device_code":      "dev-XYZ",
				"user_code":        "ABCD-1234",
				"verification_uri": "https://example.test/cli/auth",
				"expires_in":       600,
				"interval":         1,
			},
		})
	})
	mux.HandleFunc("/api/cli/device-auth-poll", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"status":       "authorized",
				"access_token": "tok-xyz",
				"user_id":      99,
				"username":     "alice",
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var menuUserCode string
	creds, err := runDeviceAuth(context.Background(), srv.URL, func(code string) {
		menuUserCode = code
	})
	if err != nil {
		t.Fatalf("runDeviceAuth: %v", err)
	}
	if creds == nil {
		t.Fatal("nil creds on success")
	}
	if creds.AccessToken != "tok-xyz" || creds.Username != "alice" {
		t.Errorf("creds = %+v", creds)
	}
	if menuUserCode != "ABCD-1234" {
		t.Errorf("menubarUI got %q, want ABCD-1234", menuUserCode)
	}
	if len(*browser) != 1 || !strings.Contains((*browser)[0], "code=ABCD-1234") {
		t.Errorf("browser opens = %v", *browser)
	}
}

func TestRunDeviceAuth_Denied(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubOpenBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/device-auth-start", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"device_code": "dev-x", "user_code": "X", "verification_uri": "https://example.test", "expires_in": 600, "interval": 1,
			},
		})
	})
	mux.HandleFunc("/api/cli/device-auth-poll", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"status": "denied"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := runDeviceAuth(context.Background(), srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("err = %v, want 'denied'", err)
	}
}

// TestHandleOpenWeb covers both the logged-in (uses creds.APIBase)
// and logged-out (falls back to DefaultAPIBase) branches.
func TestHandleOpenWeb(t *testing.T) {
	browser := stubOpenBrowser(t)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: "https://api.example.test", AccessToken: "tok"}
	c.handleOpenWeb()
	c.creds = nil
	c.handleOpenWeb()

	if len(*browser) != 2 {
		t.Fatalf("expected 2 browser opens, got %v", *browser)
	}
}
