package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartSellerGeminiOAuth_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/seller/gemini/oauth/start" {
			t.Errorf("got %s %s, want POST /api/seller/gemini/oauth/start", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":{"authorize_url":"https://accounts.google.com/o/oauth2/v2/auth?x=1","state":"abc"}}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		StartSellerGeminiOAuth(context.Background(), "gem", "gemini-pro", "http://127.0.0.1:54321/callback")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.AuthorizeURL == "" || got.State != "abc" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestStartSellerGeminiOAuth_SurfacesValidationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Matches the backend's loopback validator output — making the
		// substring assertion below resilient to message phrasing tweaks.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"success":false,"message":"redirect_uri: host must be 127.0.0.1 or localhost"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		StartSellerGeminiOAuth(context.Background(), "n", "m", "http://evil.com:5555/callback")
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1 or localhost") {
		t.Fatalf("want host-validation error, got %v", err)
	}
}

func TestCompleteSellerGeminiOAuth_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/seller/gemini/oauth/complete" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":{"channel":{"id":314},"expires_at":"2026-06-01T00:00:00Z","last_refresh":"2026-05-19T12:34:56Z"}}`)
	}))
	defer srv.Close()
	got, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		CompleteSellerGeminiOAuth(context.Background(), "code", "state")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.ChannelID != 314 {
		t.Errorf("channel id = %d, want 314", got.ChannelID)
	}
}

func TestCompleteSellerGeminiOAuth_StateMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":false,"message":"state mismatch"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		CompleteSellerGeminiOAuth(context.Background(), "code", "wrong")
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("want state-mismatch error, got %v", err)
	}
}

// TestGeminiOAuth_CookieJarReplaysAcrossStartAndComplete: same
// guarantee as the codex/claude versions — start/complete have to
// land in the same session, which only happens if the cookie jar
// replays the session cookie set by /start.
func TestGeminiOAuth_CookieJarReplaysAcrossStartAndComplete(t *testing.T) {
	var completeSawCookie bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/seller/gemini/oauth/start":
			http.SetCookie(w, &http.Cookie{Name: "everyapi_session", Value: "gem-sess", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":true,"data":{"authorize_url":"u","state":"s"}}`)
		case "/api/seller/gemini/oauth/complete":
			if c, _ := r.Cookie("everyapi_session"); c != nil && c.Value == "gem-sess" {
				completeSawCookie = true
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":false,"message":"state mismatch"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	client := New(srv.URL, "tok").WithUserID(7).WithCookieJar()
	if _, err := client.StartSellerGeminiOAuth(context.Background(), "n", "m", "http://127.0.0.1:55555/callback"); err != nil {
		t.Fatalf("start: %v", err)
	}
	_, _ = client.CompleteSellerGeminiOAuth(context.Background(), "code", "state")
	if !completeSawCookie {
		t.Fatal("session cookie was not replayed to complete — gemini flow can't survive without it")
	}
}
