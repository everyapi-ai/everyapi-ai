package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestStartSellerCodexOAuth_HappyPath: starts a flow, checks the
// envelope-mapping is correct (verification_uri / user_code /
// flow_id / interval propagate). Also asserts the request method +
// path so the CLI doesn't silently send to the wrong endpoint.
func TestStartSellerCodexOAuth_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/seller/codex/device/start" {
			t.Errorf("got %s %s, want POST /api/seller/codex/device/start", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"success": true,
			"data": {
				"flow_id": "abc",
				"user_code": "XYZ-789",
				"verification_uri": "https://chatgpt.com/codex",
				"interval": 5,
				"expires_in": 600
			}
		}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		StartSellerCodexOAuth(context.Background(), "my-pool", "gpt-4")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.FlowID != "abc" || got.UserCode != "XYZ-789" || got.Interval != 5 {
		t.Errorf("unexpected payload: %+v", got)
	}
}

// TestSellerCodexPoll_StateClassification: the CLI poll loop branches
// on State, so the wire-to-State mapping has to be airtight for each
// of pending / slow_down / expired / denied / authorized.
func TestSellerCodexPoll_StateClassification(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    SellerCodexPollState
		wantID  int
	}{
		{"pending", `{"success":false,"code":"pending"}`, SellerCodexPollPending, 0},
		{"slow_down", `{"success":false,"code":"slow_down"}`, SellerCodexPollSlowDown, 0},
		{"expired", `{"success":false,"code":"expired"}`, SellerCodexPollExpired, 0},
		{"denied", `{"success":false,"code":"denied"}`, SellerCodexPollDenied, 0},
		{
			"authorized",
			`{"success":true,"data":{"channel":{"id":42},"email":"x@y.com","account_id":"acc-1"}}`,
			SellerCodexPollAuthorized,
			42,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, c.body)
			}))
			defer srv.Close()
			got, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
				SellerCodexPoll(context.Background(), "abc")
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if got.State != c.want {
				t.Errorf("state = %d, want %d", got.State, c.want)
			}
			if got.ChannelID != c.wantID {
				t.Errorf("channel id = %d, want %d", got.ChannelID, c.wantID)
			}
		})
	}
}

// TestPollSellerCodexUntilDone_SuccessAfterPending: walks the poll
// loop through one pending tick then an authorized response, asserts
// the loop returns the authorized payload. Uses interval=1 so the
// test isn't slow.
func TestPollSellerCodexUntilDone_SuccessAfterPending(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			io.WriteString(w, `{"success":false,"code":"pending"}`)
			return
		}
		io.WriteString(w, `{"success":true,"data":{"channel":{"id":42},"email":"x@y.com"}}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		PollSellerCodexUntilDone(ctx, "abc", 1)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.State != SellerCodexPollAuthorized || got.ChannelID != 42 {
		t.Errorf("unexpected result: %+v", got)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("server hit count = %d, want 2 (pending then authorized)", hits)
	}
}

func TestPollSellerCodexUntilDone_ExpiredSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":false,"code":"expired"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		PollSellerCodexUntilDone(context.Background(), "abc", 1)
	if err != ErrSellerCodexPollExpired {
		t.Errorf("want ErrSellerCodexPollExpired, got %v", err)
	}
}

func TestPollSellerCodexUntilDone_DeniedSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":false,"code":"denied"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		PollSellerCodexUntilDone(context.Background(), "abc", 1)
	if err != ErrSellerCodexPollDenied {
		t.Errorf("want ErrSellerCodexPollDenied, got %v", err)
	}
}

// TestPollSellerCodexUntilDone_5xxBailsImmediately: a backend 5xx is
// a definitive server-side error (gateway timeout, panic, db down).
// The poll loop's transient-retry budget exists for socket-level
// blips, NOT for the server actively saying "no" — keep retrying
// would just hammer a broken backend. The *APIError fast-path in
// PollSellerCodexUntilDone must therefore return the first 5xx
// without falling into the retry loop.
func TestPollSellerCodexUntilDone_5xxBailsImmediately(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"success":false,"message":"db down"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		PollSellerCodexUntilDone(context.Background(), "abc", 1)
	if err == nil {
		t.Fatal("expected an error on persistent 5xx")
	}
	// IsUnauthorized would be false; we just need *APIError, which the
	// poll loop's `errors.As(err, new(*APIError))` already short-circuits
	// on. Counter check: exactly one server hit (no retry).
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("server hits = %d, want 1 — a 5xx must not be retried", got)
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != 500 {
		t.Errorf("want *APIError with status 500, got %v", err)
	}
}

// TestSellerCodexPoll_UnknownCodeIsError: backend's switch-default
// surfaces an unknown poll status as a non-success envelope with no
// recognised `code`. The CLI must NOT treat this as "keep polling"
// (would loop forever) or "authorized" (would attempt to read a
// zero channel id). It must error out so the user sees the failure.
func TestSellerCodexPoll_UnknownCodeIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":false,"code":"bogus","message":"server told us something weird"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").WithUserID(7).WithCookieJar().
		SellerCodexPoll(context.Background(), "abc")
	if err == nil {
		t.Fatal("unknown code must surface as error")
	}
}

// TestWithCookieJar_PersistsCookiesAcrossCalls: the OAuth flow's
// /poll endpoint depends on the cookie set by /start. Drop the jar
// and the second call lands in a fresh session — verify the jar is
// actually doing its job by checking the second request carries the
// cookie the first response set.
func TestWithCookieJar_PersistsCookiesAcrossCalls(t *testing.T) {
	var secondCallSawCookie bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/seller/codex/device/start" {
			http.SetCookie(w, &http.Cookie{Name: "everyapi_session", Value: "abc123", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":true,"data":{"flow_id":"f","user_code":"c","verification_uri":"u","interval":1,"expires_in":600}}`)
			return
		}
		if r.URL.Path == "/api/seller/codex/device/poll" {
			if cookie, _ := r.Cookie("everyapi_session"); cookie != nil && cookie.Value == "abc123" {
				secondCallSawCookie = true
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":false,"code":"expired"}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	client := New(srv.URL, "tok").WithUserID(7).WithCookieJar()
	if _, err := client.StartSellerCodexOAuth(context.Background(), "n", "m"); err != nil {
		t.Fatalf("start: %v", err)
	}
	_, _ = client.SellerCodexPoll(context.Background(), "f")
	if !secondCallSawCookie {
		t.Fatal("cookie set by /start was not replayed to /poll — jar is broken")
	}
}
