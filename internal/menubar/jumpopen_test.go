package menubar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

func stubConfirmDialog(t *testing.T, result bool) {
	t.Helper()
	prev := confirmDialog
	confirmDialog = func(title, body, confirmLabel, cancelLabel string) (bool, error) {
		return result, nil
	}
	t.Cleanup(func() { confirmDialog = prev })
}

func TestOpenViaJumpPhrase_HappyPath(t *testing.T) {
	stubConfirmDialog(t, true)
	browser := stubOpenBrowser(t)

	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/jump-session", func(w http.ResponseWriter, r *http.Request) {
		// URL must match the dashboard origin derived from the
		// caller's APIBase — see R2 (validateJumpURL). httptest's
		// URL is non-api.* so WebOriginFromBase returns it unchanged.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"session_id":          "sess-1",
				"url":                 srvURL + "/wallet?session=sess-1",
				"verification_phrase": "🐶🌟🎵🔵",
				"expires_in":          120,
			},
		})
	})
	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.state = StateLoggedIn

	c.openViaJumpPhrase("topup", "Top up")

	if len(*browser) != 1 || !strings.Contains((*browser)[0], "/wallet") {
		t.Errorf("browser opens = %v", *browser)
	}
}

func TestOpenViaJumpPhrase_UserCancels(t *testing.T) {
	stubConfirmDialog(t, false)
	browser := stubOpenBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/jump-session", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"url": "https://example.test/wallet", "verification_phrase": "x", "expires_in": 60},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.state = StateLoggedIn

	c.openViaJumpPhrase("topup", "Top up")

	if len(*browser) != 0 {
		t.Errorf("browser opened after cancel: %v", *browser)
	}
}

func TestOpenViaJumpPhrase_NotSignedIn(t *testing.T) {
	stubConfirmDialog(t, true)
	browser := stubOpenBrowser(t)
	c := newForTest(&fakeMenu{})
	c.openViaJumpPhrase("topup", "Top up")
	if len(*browser) != 0 {
		t.Errorf("browser opened when not signed in: %v", *browser)
	}
}

func TestValidateJumpURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		origin  string
		wantErr bool
	}{
		{"happy https", "https://app.everyapi.ai/wallet?session=x", "https://app.everyapi.ai", false},
		{"empty rejected", "", "https://app.everyapi.ai", true},
		{"javascript scheme rejected", "javascript:alert(1)", "https://app.everyapi.ai", true},
		{"file scheme rejected", "file:///etc/passwd", "https://app.everyapi.ai", true},
		{"vbscript scheme rejected", "vbscript:msgbox(1)", "https://app.everyapi.ai", true},
		{"different host rejected", "https://evil.example/wallet", "https://app.everyapi.ai", true},
		{"http downgrade from https origin rejected", "http://app.everyapi.ai/wallet", "https://app.everyapi.ai", true},
		{"http allowed when origin is http (self-host dev)", "http://localhost:3000/wallet", "http://localhost:3000", false},
		{"case-insensitive host match", "https://APP.EVERYAPI.AI/wallet", "https://app.everyapi.ai", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateJumpURL(tc.raw, tc.origin)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateJumpURL(%q,%q) err = %v, wantErr = %v",
					tc.raw, tc.origin, err, tc.wantErr)
			}
		})
	}
}

// TestOpenViaJumpPhrase_RejectsBadURL ensures the URL guard is wired
// into the actual happy-path flow — a backend that hands back a
// javascript: URL must NOT cause openBrowser to fire.
func TestOpenViaJumpPhrase_RejectsBadURL(t *testing.T) {
	stubConfirmDialog(t, true)
	browser := stubOpenBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/jump-session", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"url":                 "javascript:alert('p0wn')",
				"verification_phrase": "🐶🌟🎵🔵",
				"expires_in":          60,
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.state = StateLoggedIn

	c.openViaJumpPhrase("topup", "Top up")

	if len(*browser) != 0 {
		t.Errorf("browser opened with javascript: URL: %v", *browser)
	}
}

// TestOpenViaJumpPhrase_FailClosedNoDialog ensures the anti-phishing
// modal must succeed before the browser opens — if the platform's
// dialog is unsupported (Linux without zenity/kdialog), we don't
// silently proceed.
func TestOpenViaJumpPhrase_FailClosedNoDialog(t *testing.T) {
	prev := confirmDialog
	confirmDialog = func(title, body, ok, cancel string) (bool, error) {
		return false, errConfirmDialogUnsupported
	}
	t.Cleanup(func() { confirmDialog = prev })
	browser := stubOpenBrowser(t)
	notes := captureNotifier(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/jump-session", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"url":                 "https://app.everyapi.ai/wallet",
				"verification_phrase": "🐶🌟🎵🔵",
				"expires_in":          60,
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.state = StateLoggedIn

	c.openViaJumpPhrase("topup", "Top up")

	if len(*browser) != 0 {
		t.Errorf("browser opened even though dialog was unsupported: %v", *browser)
	}
	if len(*notes) == 0 {
		t.Error("expected unsupported-dialog notification")
	}
}
