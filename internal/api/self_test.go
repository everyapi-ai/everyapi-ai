package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ProbeRelayToken must hit the relay auth path (GET /v1/models) and
// translate the gateway's verdict into a plain error the caller can
// classify with IsUnauthorized — that's the whole point of the
// pre-flight check in `everyapi use` / `everyapi status`.
func TestProbeRelayToken(t *testing.T) {
	t.Run("200 means the token can relay", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				t.Errorf("probe hit %q, want /v1/models", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Errorf("probe Authorization = %q, want %q", got, "Bearer tok")
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[]}`))
		}))
		defer srv.Close()
		if err := New(srv.URL, "tok").ProbeRelayToken(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("401 is reported as unauthorized", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"success":false,"message":"token invalid"}`))
		}))
		defer srv.Close()
		err := New(srv.URL, "dead").ProbeRelayToken(context.Background())
		if err == nil {
			t.Fatal("expected an error for a 401 probe")
		}
		if !IsUnauthorized(err) {
			t.Errorf("IsUnauthorized = false, want true (err=%v)", err)
		}
	})
}
