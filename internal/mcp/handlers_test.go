package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// withCredentials writes a credentials.json pointed at `apiBase` into
// XDG_CONFIG_HOME (redirected to a temp dir) so tool handlers find
// it via config.Load(). Returns the path for sanity assertions.
//
// Each test that needs credentials calls this once; t.TempDir() +
// t.Setenv handle teardown.
func withCredentials(t *testing.T, apiBase, token string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	relDir := filepath.Join(dir, "everyapi")
	if err := os.MkdirAll(relDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(relDir, "credentials.json")
	creds := map[string]any{
		"api_base":     apiBase,
		"access_token": token,
		"user_id":      42,
		"username":     "tester",
	}
	data, _ := json.Marshal(creds)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---- everyapi_status -------------------------------------------------

func TestHandleStatus_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/status":
			w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		case "/api/user/self":
			w.Write([]byte(`{"success":true,"data":{"id":42,"username":"alice","email":"a@example.com","quota":12340000,"used_quota":5670000,"request_count":1234,"seller_quota":0}}`))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	out, err := handleStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	// We don't pin exact formatting; we DO pin the key facts: name,
	// email, both USD numbers, requests, and the trimmed wallet URL.
	wants := []string{"alice", "a@example.com", "$24.68", "$11.34", "1234"}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n  got: %s", w, out)
		}
	}
}

func TestHandleStatus_NotLoggedIn(t *testing.T) {
	// XDG_CONFIG_HOME set to a fresh empty dir → config.Load returns
	// ErrNoCredentials → handler returns errNotLoggedIn.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := handleStatus(context.Background(), nil)
	if err == nil {
		t.Fatal("want not-logged-in error")
	}
	if !strings.Contains(err.Error(), "everyapi login") {
		t.Errorf("error should mention `everyapi login`: %q", err.Error())
	}
}

func TestHandleStatus_UnauthorizedHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			w.WriteHeader(401)
			w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
		}
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	_, err := handleStatus(context.Background(), nil)
	if err == nil {
		t.Fatal("want session-expired error")
	}
	if !strings.Contains(err.Error(), "session expired") {
		t.Errorf("error should suggest re-login on 401: %q", err.Error())
	}
}

// ---- everyapi_topup --------------------------------------------------

func TestHandleTopup_TrimsAPIPrefix(t *testing.T) {
	withCredentials(t, "https://api.everyapi.ai", "tok")
	out, err := handleTopup(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "https://app.everyapi.ai/wallet") {
		t.Errorf("topup URL should point at the dashboard host: %q", out)
	}
	if strings.Contains(out, "api.everyapi.ai") {
		t.Errorf("topup URL still points at API host: %q", out)
	}
}

func TestHandleTopup_PassesThroughLocalhost(t *testing.T) {
	// Local-dev / self-hosted bases without an `api.` prefix should
	// pass through unchanged — the trim heuristic is targeted at
	// the production split, not a generic rewrite.
	withCredentials(t, "http://localhost:8787", "tok")
	out, err := handleTopup(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "http://localhost:8787/wallet") {
		t.Errorf("local-dev URL changed: %q", out)
	}
}

// ---- everyapi_seller_list --------------------------------------------

func TestHandleSellerList_TwoChannels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/seller/channel" {
			http.Error(w, "unexpected path", 404)
			return
		}
		w.Write([]byte(`{"success":true,"data":{"items":[
			{"id":11,"name":"openai-1","type":1,"status":1,"models":"gpt-4o,gpt-4o-mini"},
			{"id":12,"name":"claude-1","type":14,"status":3,"models":"claude-sonnet-4"}
		],"total":2,"page":1,"page_size":50}}`))
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	out, err := handleSellerList(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Don't pin formatting too tightly; check the per-row anchors.
	wants := []string{"2 seller channel(s)", "#11", "openai-1", "#12", "claude-1", "enabled", "disabled (auto)"}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n  got: %s", w, out)
		}
	}
}

func TestHandleSellerList_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"items":[],"total":0,"page":1,"page_size":50}}`))
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	out, err := handleSellerList(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No seller channels") {
		t.Errorf("empty-list message missing: %q", out)
	}
}

// ---- everyapi_seller_withdraw ----------------------------------------
//
// Every test in this block passes `confirm: "yes"` because the
// handler now refuses requests without it. The first test below
// — TestHandleSellerWithdraw_RejectsMissingConfirm — proves the
// guard's negative case so a regression that silently disables
// the friction step gets caught.

func TestHandleSellerWithdraw_RejectsMissingConfirm(t *testing.T) {
	// No HTTP server: the guard must short-circuit before any
	// network call. If the test fails with "connection refused"
	// rather than the expected sentinel, the guard regressed.
	withCredentials(t, "http://no-server.invalid", "tok")

	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"nil args", nil},
		{"empty object", json.RawMessage(`{}`)},
		{"explicit-amount, no confirm", json.RawMessage(`{"quota":500000}`)},
		{"wrong confirm value", json.RawMessage(`{"confirm":"YES"}`)},
		{"empty confirm string", json.RawMessage(`{"confirm":""}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handleSellerWithdraw(context.Background(), tc.raw)
			if err == nil {
				t.Fatal("expected confirm-guard error, got nil")
			}
			if !strings.Contains(err.Error(), `confirm: "yes"`) {
				t.Errorf("error should mention the required confirm value, got: %q", err.Error())
			}
		})
	}
}

func TestHandleSellerWithdraw_DefaultAll(t *testing.T) {
	var transferred atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/self":
			// Pending seller balance = 1,500,000 DB units = $3.00.
			w.Write([]byte(`{"success":true,"data":{"id":42,"username":"alice","seller_quota":1500000}}`))
		case "/api/status":
			w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		case "/api/user/seller_transfer":
			var body struct{ Quota int }
			_ = json.NewDecoder(r.Body).Decode(&body)
			transferred.Store(int64(body.Quota))
			w.Write([]byte(`{"success":true}`))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	out, err := handleSellerWithdraw(context.Background(), json.RawMessage(`{"confirm":"yes"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := transferred.Load(); got != 1500000 {
		t.Errorf("server received quota=%d, want full 1500000", got)
	}
	if !strings.Contains(out, "$3.00") {
		t.Errorf("transferred message lacks $3.00: %q", out)
	}
}

func TestHandleSellerWithdraw_ExplicitAmount(t *testing.T) {
	var transferred atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		case "/api/user/seller_transfer":
			var body struct{ Quota int }
			_ = json.NewDecoder(r.Body).Decode(&body)
			transferred.Store(int64(body.Quota))
			w.Write([]byte(`{"success":true}`))
		default:
			// /api/user/self should NOT be hit when an explicit
			// amount is provided — the "look up pending balance"
			// path is the default-all branch only.
			t.Errorf("unexpected request to %s when amount was explicit", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	_, err := handleSellerWithdraw(context.Background(), json.RawMessage(`{"confirm":"yes","quota":500000}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := transferred.Load(); got != 500000 {
		t.Errorf("server received quota=%d, want 500000", got)
	}
}

func TestHandleSellerWithdraw_NothingToWithdraw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/self" {
			w.Write([]byte(`{"success":true,"data":{"id":42,"username":"alice","seller_quota":0}}`))
			return
		}
		t.Errorf("unexpected request to %s — handler should short-circuit on zero pending", r.URL.Path)
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	out, err := handleSellerWithdraw(context.Background(), json.RawMessage(`{"confirm":"yes"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nothing to withdraw") {
		t.Errorf("expected nothing-to-withdraw message, got: %q", out)
	}
}

// ---- isJSONNull / arg parsing helpers -------------------------------

func TestIsJSONNull(t *testing.T) {
	tests := []struct {
		in   json.RawMessage
		want bool
	}{
		{nil, true},
		{json.RawMessage(""), true},
		{json.RawMessage("null"), true},
		{json.RawMessage("  null  "), true},
		{json.RawMessage("{}"), false},
		{json.RawMessage(`{"quota":1}`), false},
	}
	for _, tc := range tests {
		if got := isJSONNull(tc.in); got != tc.want {
			t.Errorf("isJSONNull(%q) = %v, want %v", string(tc.in), got, tc.want)
		}
	}
}
