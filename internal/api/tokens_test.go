package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListTokens(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.Path != "/api/token/" {
				t.Errorf("got %s %s, want GET /api/token/", r.Method, r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer acc" {
				t.Errorf("Authorization = %q, want %q", got, "Bearer acc")
			}
			if got := r.Header.Get("EveryAPI-User-Id"); got != "7" {
				t.Errorf("EveryAPI-User-Id = %q, want 7 (UserAuth needs it)", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"message":"","data":{"items":[
				{"id":2,"name":"test","status":1},
				{"id":9,"name":"old","status":2}]}}`))
		}))
		defer srv.Close()

		toks, err := New(srv.URL, "acc").WithUserID(7).ListTokens(context.Background())
		if err != nil {
			t.Fatalf("ListTokens: %v", err)
		}
		if len(toks) != 2 || toks[0].ID != 2 || toks[0].Status != TokenStatusEnabled || toks[1].Status != 2 {
			t.Errorf("unexpected tokens: %+v", toks)
		}
	})

	// Backends sometimes signal a soft failure as HTTP 200 with
	// `success:false` in the EveryAPI envelope (validation errors,
	// missing scopes). `do` returns nil for 2xx, so the `!Success`
	// branch in ListTokens is the only thing that turns this into an
	// error — keep it covered.
	t.Run("success:false at 200 surfaces the message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":false,"message":"permission denied","data":{"items":null}}`))
		}))
		defer srv.Close()
		_, err := New(srv.URL, "acc").WithUserID(7).ListTokens(context.Background())
		if err == nil {
			t.Fatal("expected an error for success:false")
		}
		if !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("error %q does not include server message", err)
		}
	})
}

func TestTokenKey(t *testing.T) {
	t.Run("returns the full key", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/api/token/2/key" {
				t.Errorf("got %s %s, want POST /api/token/2/key", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"data":{"key":"sk-everyapi-qoxegc6b3uxg9v6"}}`))
		}))
		defer srv.Close()
		key, err := New(srv.URL, "acc").WithUserID(7).TokenKey(context.Background(), 2)
		if err != nil {
			t.Fatalf("TokenKey: %v", err)
		}
		if key != "sk-everyapi-qoxegc6b3uxg9v6" {
			t.Errorf("key = %q", key)
		}
	})

	t.Run("401 is unauthorized", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"success":false,"message":"user ID not provided"}`))
		}))
		defer srv.Close()
		_, err := New(srv.URL, "acc").TokenKey(context.Background(), 2)
		if err == nil || !IsUnauthorized(err) {
			t.Errorf("want unauthorized error, got %v", err)
		}
	})

	// 200 with success:false (or success:true + empty key) — same
	// soft-failure pattern as ListTokens. The empty-key check catches
	// a server bug where the envelope is fine but the payload is
	// missing the key field; we'd rather error than hand the user an
	// empty ANTHROPIC_AUTH_TOKEN.
	t.Run("success:false at 200 is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":false,"message":"token not found","data":{"key":""}}`))
		}))
		defer srv.Close()
		_, err := New(srv.URL, "acc").WithUserID(7).TokenKey(context.Background(), 99)
		if err == nil {
			t.Fatal("expected an error for success:false")
		}
		if !strings.Contains(err.Error(), "token not found") {
			t.Errorf("error %q does not include server message", err)
		}
	})

	t.Run("empty key at 200 is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"data":{"key":""}}`))
		}))
		defer srv.Close()
		_, err := New(srv.URL, "acc").WithUserID(7).TokenKey(context.Background(), 1)
		if err == nil {
			t.Fatal("expected an error for empty key payload")
		}
	})
}
