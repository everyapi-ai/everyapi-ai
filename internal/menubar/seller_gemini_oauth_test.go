package menubar

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// TestRunGeminiOAuth_HappyPath drives the full loopback flow end-to-end:
// the fake backend reads the redirect_uri the controller wrote, then
// posts back to the loopback listener to simulate Google's redirect.
// /complete returns a synthetic channel id; the test asserts the
// success notification fires.
func TestRunGeminiOAuth_HappyPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubTextPrompt(t, []string{"gemini-test", "gemini-2.0-pro"})
	browser := stubOpenBrowser(t)
	notes := captureNotifier(t)

	const fakeState = "stub-state-xyz"
	var completeHit atomic.Int32
	var loopbackRedirect string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/seller/gemini/oauth/start", func(w http.ResponseWriter, r *http.Request) {
		// Capture the redirect_uri the controller sent so we can
		// hit it from the test side to simulate Google's redirect.
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		_ = json.Unmarshal(body, &payload)
		loopbackRedirect = payload["redirect_uri"]
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"authorize_url": "https://accounts.google.com/oauth?stub=1",
				"state":         fakeState,
			},
		})
		// Async: simulate Google redirecting the browser to our
		// loopback listener with code + state. Tiny delay so the
		// controller has time to call Wait().
		go func() {
			cb := loopbackRedirect + "?code=the-google-code&state=" + fakeState
			_, _ = http.Get(cb)
		}()
	})
	mux.HandleFunc("/api/seller/gemini/oauth/complete", func(w http.ResponseWriter, r *http.Request) {
		completeHit.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"channel":      map[string]interface{}{"id": 77},
				"expires_at":   "2027-01-01T00:00:00Z",
				"last_refresh": "2026-05-19T00:00:00Z",
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.state = StateLoggedIn

	if err := c.runGeminiOAuth(t.Context()); err != nil {
		t.Fatalf("runGeminiOAuth: %v", err)
	}

	if completeHit.Load() != 1 {
		t.Errorf("/complete called %d times, want 1", completeHit.Load())
	}
	if len(*browser) != 1 || !strings.Contains((*browser)[0], "accounts.google.com") {
		t.Errorf("browser opens = %v", *browser)
	}
	// Loopback URL must have been a localhost ephemeral port.
	u, err := url.Parse(loopbackRedirect)
	if err != nil {
		t.Fatalf("parse loopback URL %q: %v", loopbackRedirect, err)
	}
	if !strings.HasPrefix(u.Host, "127.0.0.1:") {
		t.Errorf("loopback host = %q, want 127.0.0.1:port", u.Host)
	}
	if len(*notes) == 0 || !strings.Contains((*notes)[len(*notes)-1].title, "Gemini channel #77") {
		t.Errorf("expected success notification, got %+v", *notes)
	}
}

// TestHandleAddGemini_NotSignedIn ensures the controller-level
// wrapper short-circuits when there are no creds.
func TestHandleAddGemini_NotSignedIn(t *testing.T) {
	notes := captureNotifier(t)
	c := newForTest(&fakeMenu{})
	c.handleAddGemini()
	if len(*notes) == 0 {
		t.Error("expected failure notification when not signed in")
	}
}

// TestRunGeminiOAuth_StateMismatch verifies CSRF defence: if the
// loopback callback brings a `state` that doesn't match the one
// /start handed out, runGeminiOAuth returns an error and skips
// /complete entirely.
func TestRunGeminiOAuth_StateMismatch(t *testing.T) {
	stubTextPrompt(t, []string{"gemini-test", "gemini-2.0-pro"})
	stubOpenBrowser(t)

	var completeHit atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/seller/gemini/oauth/start", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		_ = json.Unmarshal(body, &payload)
		loopback := payload["redirect_uri"]
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"authorize_url": "https://accounts.google.com/oauth?stub=1",
				"state":         "real-state",
			},
		})
		go func() {
			// Note: wrong state on purpose.
			cb := loopback + "?code=evil&state=attacker-state"
			_, _ = http.Get(cb)
		}()
	})
	mux.HandleFunc("/api/seller/gemini/oauth/complete", func(w http.ResponseWriter, r *http.Request) {
		completeHit.Add(1)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.state = StateLoggedIn

	err := c.runGeminiOAuth(t.Context())
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("err = %v, want state-mismatch", err)
	}
	if completeHit.Load() != 0 {
		t.Errorf("/complete called %d times on CSRF — must be 0", completeHit.Load())
	}
}
