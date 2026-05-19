// Package oauthloopback runs a one-shot HTTP listener on an ephemeral
// loopback port to receive an OAuth authorization-code redirect.
// `everyapi seller add-oauth gemini` uses it: it picks a random port,
// tells the backend "redirect Google to this URL", opens the browser,
// then waits here for Google to GET /callback?code=…&state=… and
// shuts down once it has the pair.
//
// Why a small dedicated package rather than inlining in cmd:
//   - the listener has its own lifecycle (start, single-shot, stop)
//     that's easier to reason about + test in isolation;
//   - future OAuth flows (other providers, MCP server) can reuse this
//     without dragging in cmd-level dependencies.
//
// Out of scope: TLS (loopback HTTP is fine — the OAuth state is
// short-lived and never leaves localhost), persistent listeners
// (every CLI invocation is a fresh process and gets its own port).
package oauthloopback

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Result is what the listener captures from the single OAuth callback
// it serves. Code+State are the success path. Error / ErrorDesc are
// the failure path (Google sends `?error=...&error_description=...`
// when the user denies, for example).
type Result struct {
	Code      string
	State     string
	Error     string
	ErrorDesc string
}

// Listener owns a net.Listener on a random loopback port + a
// http.Server hooked to a single handler that captures the OAuth
// callback. The lifecycle is one-shot: caller starts it, waits on
// Wait, then closes. A second Wait or a Wait after Close returns
// promptly so the caller can defer Close unconditionally.
type Listener struct {
	server *http.Server
	addr   string
	port   int

	mu        sync.Mutex
	resultCh  chan Result
	closed    bool
	delivered bool // sticky: stays true once the first callback delivered
}

// Listen starts a loopback HTTP listener on a random ephemeral port.
// The returned Listener's URL is what the OAuth caller should pass
// as `redirect_uri` to the provider. Caller MUST call Close exactly
// once (defer is fine) so the listener stops and the port is freed.
func Listen() (*Listener, error) {
	// "127.0.0.1:0" asks the kernel for an unused ephemeral port. We
	// deliberately bind to 127.0.0.1 (not 0.0.0.0) so the listener
	// isn't reachable from the network — the OAuth code would be
	// readable by anyone else on the LAN otherwise.
	nl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen loopback: %w", err)
	}
	tcpAddr, ok := nl.Addr().(*net.TCPAddr)
	if !ok {
		_ = nl.Close()
		return nil, errors.New("loopback listener returned non-TCP addr (impossible?)")
	}

	l := &Listener{
		port:     tcpAddr.Port,
		resultCh: make(chan Result, 1),
	}
	l.addr = fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port)

	mux := http.NewServeMux()
	// /callback is the only route we serve. Any other request gets
	// 404 — keeps the surface tiny and predictable.
	mux.HandleFunc("/callback", l.handleCallback)
	l.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		// Serve always returns ErrServerClosed after Shutdown; swallow.
		_ = l.server.Serve(nl)
	}()
	return l, nil
}

// URL is the loopback redirect_uri the caller should pass to the
// OAuth provider. Always `http://127.0.0.1:<port>/callback` to match
// the backend's validateLoopbackRedirectURI.
func (l *Listener) URL() string {
	return "http://" + l.addr + "/callback"
}

// Port returns the port the listener bound to. Useful for logging /
// error messages; not required for the OAuth flow itself.
func (l *Listener) Port() int { return l.port }

// Wait blocks until either the callback handler captures a result OR
// the context is cancelled. The first Wait wins; subsequent Wait
// calls return immediately with the same Result (re-reading from a
// closed channel yields the zero value, so we re-deliver explicitly).
func (l *Listener) Wait(ctx context.Context) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case r := <-l.resultCh:
		return r, nil
	}
}

// Close stops the server and releases the port. Idempotent — calling
// it multiple times (e.g. via defer + explicit cleanup) is safe.
func (l *Listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	// Give Shutdown a short grace period so the success-page HTML
	// finishes flushing to the browser tab before we yank the socket.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return l.server.Shutdown(ctx)
}

func (l *Listener) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res := Result{
		Code:      q.Get("code"),
		State:     q.Get("state"),
		Error:     q.Get("error"),
		ErrorDesc: q.Get("error_description"),
	}

	// Sticky one-shot delivery: a `delivered` flag (under l.mu) stays
	// true once the first callback fired, independent of whether
	// Wait() has drained the channel buffer yet. Without the sticky
	// flag, a duplicate callback arriving AFTER Wait() returns (but
	// BEFORE Close() shuts down the HTTP server — a several-second
	// window in real flows) would successfully re-push to the empty
	// buffer and render the success template, mimicking a finished
	// flow to whoever fired the duplicate hit (browser retry, double-
	// click, or hostile local process scanning loopback ports).
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	l.mu.Lock()
	if l.delivered {
		l.mu.Unlock()
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, duplicateHTML)
		return
	}
	l.delivered = true
	l.mu.Unlock()

	// Non-blocking send: the buffer is sized 1 and we've just claimed
	// the sole delivery slot under the lock above, so this case
	// always succeeds. The default branch survives as a belt-and-
	// suspenders guard against a stuck channel writer.
	select {
	case l.resultCh <- res:
	default:
	}
	if res.Error != "" || res.Code == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, errorHTML(res))
		return
	}
	fmt.Fprint(w, successHTML)
}

const duplicateHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>EveryAPI — callback already received</title>
<style>body{font-family:system-ui,-apple-system,sans-serif;max-width:540px;margin:6rem auto;padding:0 1rem;color:#222}</style>
</head><body>
<h1>Already received</h1>
<p>The authorization callback was already received and processed. You can close this tab.</p>
<p>If you didn't open this page yourself, something else on your machine just hit the EveryAPI loopback listener — close the tab, return to the terminal, and re-run the command from scratch.</p>
</body></html>`

const successHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>EveryAPI — authorization complete</title>
<style>body{font-family:system-ui,-apple-system,sans-serif;max-width:540px;margin:6rem auto;padding:0 1rem;color:#222}</style>
</head><body>
<h1>Authorization complete</h1>
<p>You can close this tab and return to the terminal — EveryAPI is finishing the channel mount.</p>
</body></html>`

func errorHTML(r Result) string {
	desc := r.ErrorDesc
	if desc == "" {
		if r.Error != "" {
			desc = r.Error
		} else {
			desc = "the OAuth provider didn't send an authorization code"
		}
	}
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>EveryAPI — authorization failed</title>
<style>body{font-family:system-ui,-apple-system,sans-serif;max-width:540px;margin:6rem auto;padding:0 1rem;color:#222}.err{color:#b00}</style>
</head><body>
<h1>Authorization failed</h1>
<p class="err">` + htmlEscape(desc) + `</p>
<p>Close this tab and return to the terminal; you can re-run the command to try again.</p>
</body></html>`
}

// htmlEscape is a tiny manual escape — we only render OAuth provider
// error strings, so the input shape is narrow and dragging in
// html/template for one substitution is overkill.
func htmlEscape(s string) string {
	var b []byte
	for _, r := range s {
		switch r {
		case '<':
			b = append(b, "&lt;"...)
		case '>':
			b = append(b, "&gt;"...)
		case '&':
			b = append(b, "&amp;"...)
		case '"':
			b = append(b, "&quot;"...)
		case '\'':
			b = append(b, "&#39;"...)
		default:
			b = append(b, string(r)...)
		}
	}
	return string(b)
}
