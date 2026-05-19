package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartSellerClaudeOAuth_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/seller/claude/oauth/start" {
			t.Errorf("got %s %s, want POST /api/seller/claude/oauth/start", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":{"authorize_url":"https://claude.ai/oauth/authorize?x=1"}}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		StartSellerClaudeOAuth(context.Background(), "claude-pro", "claude-3-opus")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "https://claude.ai/oauth/authorize?x=1" {
		t.Errorf("unexpected url: %q", got)
	}
}

func TestStartSellerClaudeOAuth_PropagatesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":false,"message":"verify your email"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		StartSellerClaudeOAuth(context.Background(), "n", "m")
	if err == nil || !strings.Contains(err.Error(), "verify your email") {
		t.Fatalf("want error containing 'verify your email', got %v", err)
	}
}

func TestCompleteSellerClaudeOAuth_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/seller/claude/oauth/complete" {
			t.Errorf("got %s %s, want POST /api/seller/claude/oauth/complete", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"success": true,
			"data": {
				"channel": {"id": 42},
				"expires_at": "2026-06-01T00:00:00Z",
				"last_refresh": "2026-05-19T12:34:56Z"
			}
		}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		CompleteSellerClaudeOAuth(context.Background(), "code#state")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.ChannelID != 42 {
		t.Errorf("channel id = %d, want 42", got.ChannelID)
	}
	if got.ExpiresAt != "2026-06-01T00:00:00Z" {
		t.Errorf("expires = %q", got.ExpiresAt)
	}
}

// TestCompleteSellerClaudeOAuth_StateMismatch — backend surfaces this
// as success:false with a specific message. The CLI must propagate
// it verbatim so the user knows to retry with a fresh /start.
func TestCompleteSellerClaudeOAuth_StateMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":false,"message":"state mismatch"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		CompleteSellerClaudeOAuth(context.Background(), "code#wrongstate")
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("want state-mismatch error, got %v", err)
	}
}

// Cookie-jar smoke test — claude flow is start→complete in the same
// CLI invocation, same session cookie requirement as codex. Already
// covered by TestWithCookieJar_PersistsCookiesAcrossCalls in
// seller_oauth_test.go but redo it through the claude endpoints in
// case the codex path is later refactored.
func TestClaudeOAuth_CookieJarReplaysAcrossStartAndComplete(t *testing.T) {
	var completeSawCookie bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/seller/claude/oauth/start":
			http.SetCookie(w, &http.Cookie{Name: "everyapi_session", Value: "claude-sess", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":true,"data":{"authorize_url":"https://x"}}`)
		case "/api/seller/claude/oauth/complete":
			if cookie, _ := r.Cookie("everyapi_session"); cookie != nil && cookie.Value == "claude-sess" {
				completeSawCookie = true
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":false,"message":"oauth flow not started or session expired"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	client := New(srv.URL, "tok").WithUserID(7).WithCookieJar()
	if _, err := client.StartSellerClaudeOAuth(context.Background(), "n", "m"); err != nil {
		t.Fatalf("start: %v", err)
	}
	_, _ = client.CompleteSellerClaudeOAuth(context.Background(), "code#state")
	if !completeSawCookie {
		t.Fatal("session cookie was not replayed to complete — claude flow can't survive without it")
	}
}
