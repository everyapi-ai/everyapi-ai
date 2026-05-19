package oauthloopback

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestListener_CapturesCodeAndState(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	if l.Port() == 0 {
		t.Fatal("port not assigned")
	}
	if !strings.HasPrefix(l.URL(), "http://127.0.0.1:") || !strings.HasSuffix(l.URL(), "/callback") {
		t.Errorf("URL shape wrong: %q", l.URL())
	}

	go func() {
		// Give Listen a beat to start the goroutine — Serve runs in
		// the background and may not have ServeHTTP'd before our
		// client request lands. 50ms is more than enough on any
		// reasonable machine and keeps the test fast.
		time.Sleep(50 * time.Millisecond)
		_, _ = http.Get(l.URL() + "?code=abc&state=xyz")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := l.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Code != "abc" || res.State != "xyz" || res.Error != "" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestListener_PropagatesProviderError(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = http.Get(l.URL() + "?error=access_denied&error_description=user+denied")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := l.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Error != "access_denied" {
		t.Errorf("error = %q, want access_denied", res.Error)
	}
	if res.ErrorDesc != "user denied" {
		t.Errorf("error_description = %q, want 'user denied'", res.ErrorDesc)
	}
}

func TestListener_WaitContextCancel(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := l.Wait(ctx); err == nil {
		t.Fatal("Wait must return ctx.Err on timeout, got nil")
	}
}

func TestListener_CloseIsIdempotent(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Second Close must NOT panic / error — defer pattern relies on it.
	if err := l.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestListener_OnlyServesCallback: a stray request to /anything-else
// must 404, not crash. Defensive — keeps the surface tight.
func TestListener_OnlyServesCallback(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	time.Sleep(50 * time.Millisecond)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/not-callback", l.Port()))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
