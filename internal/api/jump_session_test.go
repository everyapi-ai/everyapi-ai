package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateJumpSession_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/cli/jump-session" {
			t.Errorf("got %s %s, want POST /api/cli/jump-session", r.Method, r.URL.Path)
		}
		// Body must carry the intent so the backend knows which page to point at.
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["intent"] != "topup" {
			t.Errorf("intent = %q, want topup", body["intent"])
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"success": true,
			"data": {
				"session_id":          "abc123",
				"url":                 "https://app.everyapi.ai/wallet/topup?jump_session=abc123",
				"verification_phrase": "🌊 🦊 🍕 🚀",
				"expires_in":          600
			}
		}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "tok").WithUserID(7).CreateJumpSession(context.Background(), "topup")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.SessionID != "abc123" {
		t.Errorf("session id = %q", got.SessionID)
	}
	if !strings.Contains(got.URL, "jump_session=abc123") {
		t.Errorf("URL missing session id: %q", got.URL)
	}
	if got.VerificationPhrase == "" {
		t.Error("phrase missing")
	}
	if got.ExpiresIn != 600 {
		t.Errorf("expires_in = %d, want 600", got.ExpiresIn)
	}
}

// TestCreateJumpSession_RejectsUnknownIntent: the backend returns
// HTTP 400 + a message naming the supported intents. *APIError must
// surface that message verbatim so the CLI can echo it.
func TestCreateJumpSession_RejectsUnknownIntent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"success":false,"message":"unknown intent — supported: topup, wallet, channels"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").WithUserID(7).CreateJumpSession(context.Background(), "bogus")
	if err == nil || !strings.Contains(err.Error(), "supported: topup") {
		t.Fatalf("want intent-rejection error, got %v", err)
	}
}

// TestCreateJumpSession_PropagatesUnauthorized: an expired token
// must surface via IsUnauthorized so the cmd layer can render the
// re-login hint.
func TestCreateJumpSession_PropagatesUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"success":false,"message":"unauthorized"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").WithUserID(7).CreateJumpSession(context.Background(), "topup")
	if err == nil {
		t.Fatal("want error on 401")
	}
	if !IsUnauthorized(err) {
		t.Errorf("want IsUnauthorized=true, got %v", err)
	}
}
