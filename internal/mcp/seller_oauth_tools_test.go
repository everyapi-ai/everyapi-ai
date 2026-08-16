package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Each test resets the shared OAuth client so a previous test's long-lived jar doesn't leak — necessary because the helper is a sync.Once singleton by design. Cleanup runs after the test finishes so the next test sees a fresh Once.
func resetSharedOAuthClient(t *testing.T) {
	t.Helper()
	// Reset BEFORE the test runs too, in case a sibling test in this package already triggered the Once.
	sharedOAuthClient = nil
	sharedOAuthClientOnce = sync.Once{}
	t.Cleanup(func() {
		sharedOAuthClient = nil
		sharedOAuthClientOnce = sync.Once{}
	})
}

// ---- everyapi_seller_add_oauth_codex_start ---------------------------

func TestHandleSellerAddOAuthCodexStart_HappyPath(t *testing.T) {
	resetSharedOAuthClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/seller/codex/device/start" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":{"flow_id":"f-123","user_code":"USR-789","verification_uri":"https://chatgpt.com/codex","interval":5,"expires_in":600}}`)
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	args := mustMarshal(t, map[string]string{"name": "my-chatgpt", "models": "gpt-4"})
	out, err := handleSellerAddOAuthCodexStart(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	for _, want := range []string{"USR-789", "chatgpt.com/codex", "f-123", "codex_poll"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestHandleSellerAddOAuthCodexStart_MissingFields(t *testing.T) {
	resetSharedOAuthClient(t)
	withCredentials(t, "http://x", "tok")
	cases := []map[string]string{
		{},                              // both missing
		{"name": "c"},                   // models missing
		{"models": "gpt-4"},             // name missing
		{"name": "", "models": "gpt-4"}, // blank name
		{"name": "c", "models": "   "},  // blank models
	}
	for _, c := range cases {
		args := mustMarshal(t, c)
		_, err := handleSellerAddOAuthCodexStart(context.Background(), args)
		if err == nil {
			t.Errorf("want error for %+v, got nil", c)
		}
	}
}

// ---- everyapi_seller_add_oauth_codex_poll ----------------------------

func TestHandleSellerAddOAuthCodexPoll_AuthorizedRendersChannel(t *testing.T) {
	resetSharedOAuthClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/seller/codex/device/poll" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":{"channel":{"id":314},"email":"alice@example.com","account_id":"acc-1"}}`)
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	args := mustMarshal(t, map[string]string{"flow_id": "f-123"})
	out, err := handleSellerAddOAuthCodexPoll(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	for _, want := range []string{"authorized", "#314", "alice@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestHandleSellerAddOAuthCodexPoll_PendingTellsToWait(t *testing.T) {
	resetSharedOAuthClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":false,"code":"pending"}`)
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	args := mustMarshal(t, map[string]string{"flow_id": "f-123"})
	out, err := handleSellerAddOAuthCodexPoll(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(out, "pending") {
		t.Errorf("want pending hint, got %q", out)
	}
}

// TestHandleSellerAddOAuthCodexPoll_NonAuthorizedStates pins the distinct user-facing copy for each non-authorized poll outcome. These are not errors (err==nil) — the AI agent drives next steps off the rendered string, so the keyword has to be present.
func TestHandleSellerAddOAuthCodexPoll_NonAuthorizedStates(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"slow_down", "slow_down"},
		{"expired", "expired"},
		{"denied", "denied"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			resetSharedOAuthClient(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"success":false,"code":"`+tc.code+`"}`)
			}))
			defer srv.Close()
			withCredentials(t, srv.URL, "tok")

			out, err := handleSellerAddOAuthCodexPoll(context.Background(),
				mustMarshal(t, map[string]string{"flow_id": "f-123"}))
			if err != nil {
				t.Fatalf("state %q should not be an error: %v", tc.code, err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("state %q: output missing %q\nfull:\n%s", tc.code, tc.want, out)
			}
		})
	}
}

func TestHandleSellerAddOAuthCodexPoll_RequiresFlowID(t *testing.T) {
	resetSharedOAuthClient(t)
	withCredentials(t, "http://x", "tok")
	_, err := handleSellerAddOAuthCodexPoll(context.Background(), mustMarshal(t, map[string]string{"flow_id": ""}))
	if err == nil {
		t.Fatal("want error for blank flow_id")
	}
}

// ---- everyapi_seller_add_oauth_claude_start --------------------------

func TestHandleSellerAddOAuthClaudeStart_PrintsAuthorizeURL(t *testing.T) {
	resetSharedOAuthClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/seller/claude/oauth/start" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":{"authorize_url":"https://claude.ai/oauth/authorize?x=1"}}`)
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	args := mustMarshal(t, map[string]string{"name": "my-claude", "models": "claude-3-opus"})
	out, err := handleSellerAddOAuthClaudeStart(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(out, "https://claude.ai/oauth/authorize?x=1") {
		t.Errorf("output missing authorize URL: %s", out)
	}
	if !strings.Contains(out, "claude_complete") {
		t.Errorf("output should mention the follow-up tool name: %s", out)
	}
}

// ---- everyapi_seller_add_oauth_claude_complete -----------------------

func TestHandleSellerAddOAuthClaudeComplete_HappyPath(t *testing.T) {
	resetSharedOAuthClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/seller/claude/oauth/complete" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":{"channel":{"id":42},"expires_at":"2026-06-01T00:00:00Z"}}`)
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	args := mustMarshal(t, map[string]string{"input": "code#state"})
	out, err := handleSellerAddOAuthClaudeComplete(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(out, "#42") {
		t.Errorf("output missing channel #42: %s", out)
	}
}

func TestHandleSellerAddOAuthClaudeComplete_RequiresInput(t *testing.T) {
	resetSharedOAuthClient(t)
	withCredentials(t, "http://x", "tok")
	_, err := handleSellerAddOAuthClaudeComplete(context.Background(), mustMarshal(t, map[string]string{"input": ""}))
	if err == nil {
		t.Fatal("want error for blank input")
	}
}

// ---- shared cookie jar across tool calls ---------------------------

// TestOAuthClient_CookieJarSurvivesAcrossTools asserts the key invariant that justifies loadOAuthClient existing as a singleton: a session cookie set by one tool call MUST replay on the next. Without this, the codex/claude OAuth flows can't function.
func TestOAuthClient_CookieJarSurvivesAcrossTools(t *testing.T) {
	resetSharedOAuthClient(t)
	var secondCallSawCookie atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/seller/codex/device/start":
			http.SetCookie(w, &http.Cookie{Name: "everyapi_session", Value: "abc", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":true,"data":{"flow_id":"f","user_code":"c","verification_uri":"u","interval":1,"expires_in":600}}`)
		case "/api/seller/codex/device/poll":
			if c, _ := r.Cookie("everyapi_session"); c != nil && c.Value == "abc" {
				secondCallSawCookie.Store(true)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":false,"code":"pending"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	// First call: start (sets cookie on the shared jar).
	if _, err := handleSellerAddOAuthCodexStart(context.Background(),
		mustMarshal(t, map[string]string{"name": "n", "models": "m"})); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Second call: poll (must carry the cookie back).
	if _, err := handleSellerAddOAuthCodexPoll(context.Background(),
		mustMarshal(t, map[string]string{"flow_id": "f"})); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if !secondCallSawCookie.Load() {
		t.Fatal("the second tool call did NOT replay the session cookie — long-lived jar broken")
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
