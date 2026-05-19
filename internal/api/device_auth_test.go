package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestPollUntilDone_HappyPath: pending → pending → authorized. The
// poller must keep looping past pendings and stop on the authorized
// reply, returning the token. We exercise the loop with a 1-second
// initial interval so the test finishes in under 5 seconds.
func TestPollUntilDone_HappyPath(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			w.Write([]byte(`{"success":true,"data":{"status":"pending"}}`))
			return
		}
		w.Write([]byte(`{"success":true,"data":{"status":"authorized","access_token":"tok-xyz","user_id":42,"username":"alice"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.PollUntilDone(ctx, "dev-code", 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.AccessToken != "tok-xyz" || res.UserID != 42 || res.Username != "alice" {
		t.Errorf("unexpected result %+v", res)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 polls, got %d", calls)
	}
}

// TestPollUntilDone_Expired: server returns "expired" → poller
// surfaces ErrDeviceAuthExpired (the cmd layer renders it as a
// "took too long" message).
func TestPollUntilDone_Expired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"status":"expired"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")

	_, err := c.PollUntilDone(context.Background(), "dev-code", 1)
	if !errors.Is(err, ErrDeviceAuthExpired) {
		t.Fatalf("want ErrDeviceAuthExpired, got %v", err)
	}
}

// TestPollUntilDone_Denied: server returns "denied" → poller
// surfaces ErrDeviceAuthDenied.
func TestPollUntilDone_Denied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"status":"denied"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")

	_, err := c.PollUntilDone(context.Background(), "dev-code", 1)
	if !errors.Is(err, ErrDeviceAuthDenied) {
		t.Fatalf("want ErrDeviceAuthDenied, got %v", err)
	}
}

// TestPollUntilDone_SlowDown verifies the interval-bump path. The
// server returns slow_down once, then authorized. We don't measure
// wall-clock interval growth (flaky); we just confirm the loop
// terminates on the authorized reply after the slow_down step.
func TestPollUntilDone_SlowDown(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Write([]byte(`{"success":true,"data":{"status":"slow_down"}}`))
			return
		}
		w.Write([]byte(`{"success":true,"data":{"status":"authorized","access_token":"t","user_id":1,"username":"u"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := c.PollUntilDone(ctx, "dev-code", 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.AccessToken != "t" {
		t.Errorf("token = %q", res.AccessToken)
	}
}

// TestDeviceAuthStart_HappyPath: confirm the start response shape is
// decoded correctly (device_code, user_code, verification_uri,
// interval all surface through). Guards against future renames.
func TestDeviceAuthStart_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"device_code":      "dev-abc",
				"user_code":        "USER-CODE",
				"verification_uri": "https://everyapi.ai/cli/auth",
				"expires_in":       600,
				"interval":         5,
			},
		})
	}))
	defer srv.Close()
	c := New(srv.URL, "")

	res, err := c.DeviceAuthStart(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.DeviceCode != "dev-abc" {
		t.Errorf("DeviceCode = %q", res.DeviceCode)
	}
	if res.UserCode != "USER-CODE" {
		t.Errorf("UserCode = %q", res.UserCode)
	}
	if res.VerificationURI != "https://everyapi.ai/cli/auth" {
		t.Errorf("VerificationURI = %q", res.VerificationURI)
	}
	if res.ExpiresIn != 600 {
		t.Errorf("ExpiresIn = %d", res.ExpiresIn)
	}
	if res.Interval != 5 {
		t.Errorf("Interval = %d", res.Interval)
	}
}

// TestClient_ServerError surfaces an APIError when the server says
// success=false. The cmd layer relies on this so a server-side "bad
// request" doesn't appear as a generic "couldn't decode response".
func TestClient_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"success":false,"message":"bad code"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")

	_, err := c.DeviceAuthStart(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want APIError, got %T: %v", err, err)
	}
	if ae.StatusCode != 400 {
		t.Errorf("StatusCode = %d", ae.StatusCode)
	}
	if ae.Message != "bad code" {
		t.Errorf("Message = %q", ae.Message)
	}
}

// TestPollUntilDone_TransientRetry covers the "wifi blip" path: the
// first two poll attempts fail at the transport layer (server hung
// up on the TCP connection without sending a response), the third
// succeeds with authorized. Without the retry budget the user would
// see `everyapi login` die mid-flow; with it, the flow completes.
func TestPollUntilDone_TransientRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			// Close the underlying connection without sending a
			// reply — http.Client surfaces this as a non-API
			// transport error, which the retry budget should absorb.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("ResponseWriter does not support hijack")
			}
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.Write([]byte(`{"success":true,"data":{"status":"authorized","access_token":"t","user_id":1,"username":"u"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.PollUntilDone(ctx, "dev-code", 1)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if res.AccessToken != "t" {
		t.Errorf("token = %q", res.AccessToken)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 poll attempts, got %d", calls)
	}
}

// TestPollUntilDone_TransientBudgetExhausted: when transient errors
// outlast the retry budget, the final transport error propagates
// rather than retrying forever.
func TestPollUntilDone_TransientBudgetExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijack")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()
	c := New(srv.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := c.PollUntilDone(ctx, "dev-code", 1)
	if err == nil {
		t.Fatal("want error after transient budget exhausted")
	}
	// Must NOT be one of the protocol sentinels — those signal a
	// terminal server response.
	if errors.Is(err, ErrDeviceAuthExpired) || errors.Is(err, ErrDeviceAuthDenied) {
		t.Errorf("unexpected sentinel error: %v", err)
	}
}
