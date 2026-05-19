package oauthloopback

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// TestListener_DuplicateCallbackAfterWait closes the gap caught in
// the security re-review: relying on the buffered channel's
// non-block-send `default` branch only suppresses duplicates while
// the buffer is FULL. Once Wait() drains the result, the buffer
// empties, and a second /callback could successfully re-push and
// render the success page — exactly the spoofing scenario R7 was
// meant to prevent. The sticky `delivered` flag closes that window.
func TestListener_DuplicateCallbackAfterWait(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()
	time.Sleep(50 * time.Millisecond)

	cb := fmt.Sprintf("http://127.0.0.1:%d/callback?code=c1&state=s1", l.Port())
	r1, err := http.Get(cb)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	r1.Body.Close()
	if r1.StatusCode != 200 {
		t.Errorf("first call status = %d, want 200", r1.StatusCode)
	}

	// Drain the channel — this is what runGeminiOAuth does in
	// production right after the callback fires. Pre-fix, this
	// emptied the buffer and re-enabled the duplicate-as-success
	// path; post-fix, the sticky flag holds.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	r2, err := http.Get(cb)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != 409 {
		t.Errorf("duplicate-after-drain status = %d, want 409", r2.StatusCode)
	}
	body, _ := io.ReadAll(r2.Body)
	if bytes.Contains(body, []byte("Authorization complete")) {
		t.Error("duplicate served success template; should serve duplicate template")
	}
}

// TestListener_DuplicateCallback: a second /callback hit after the
// first delivery must NOT show the success page (an attacker who
// reached the loopback port mustn't be able to mimic a finished
// flow). Returns 409 + the "already handled" template instead.
func TestListener_DuplicateCallback(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()
	time.Sleep(50 * time.Millisecond)

	cb := fmt.Sprintf("http://127.0.0.1:%d/callback?code=c1&state=s1", l.Port())
	r1, err := http.Get(cb)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	r1.Body.Close()
	if r1.StatusCode != 200 {
		t.Errorf("first call status = %d, want 200", r1.StatusCode)
	}

	r2, err := http.Get(cb)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != 409 {
		t.Errorf("duplicate call status = %d, want 409", r2.StatusCode)
	}
	body, _ := io.ReadAll(r2.Body)
	if !bytes.Contains(body, []byte("Already received")) {
		t.Errorf("duplicate body missing 'Already received' header: %s", body)
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
