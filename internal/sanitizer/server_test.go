package sanitizer

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startServer spins up a Server bound to an ephemeral port pointing
// at the given upstream URL. Returns the proxy base ("http://...")
// and a stop func.
func startServer(t *testing.T, upstream string) (string, *Server, func()) {
	t.Helper()
	return startServerCfg(t, Config{UpstreamBase: upstream})
}

// startServerCfg is startServer with caller-supplied Config knobs
// (logger capture, AllowNonLoopback, …). Listen + a discard logger are
// filled in when the caller leaves them zero.
func startServerCfg(t *testing.T, cfg Config) (string, *Server, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cfg.Listen = addr
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	// Wait briefly for the listener to come up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop := func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Logf("server stopped with: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Logf("server didn't stop in time")
		}
	}
	return "http://" + addr, srv, stop
}

func TestServer_RewritesAnthropicRequest(t *testing.T) {
	// Upstream that echoes the request body so the test can assert
	// what bytes the proxy actually forwarded.
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"echo":"ok"}`))
	}))
	defer upstream.Close()

	base, _, stop := startServer(t, upstream.URL)
	defer stop()

	body := []byte(`{"model":"claude-opus-4-7","messages":[{"role":"user","content":"this is sk-ant-foo_abcdefghijklmnopqrstuvwxyz1234"}]}`)
	resp, err := http.Post(base+"/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if bytes.Contains(got, []byte("sk-ant-foo_abcdefghijklmnopqrstuvwxyz1234")) {
		t.Errorf("upstream saw real key: %s", got)
	}
	if !bytes.Contains(got, []byte(PlaceholderPrefix)) {
		t.Errorf("upstream didn't receive placeholder: %s", got)
	}
}

func TestServer_RestoresInResponse(t *testing.T) {
	// Upstream that responds with the placeholder embedded — the
	// proxy must restore it on the way out so the SDK sees the
	// original secret.
	var capturedPlaceholder string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Pull the placeholder out of the request body, then echo
		// it back in the response.
		idxs := FindPlaceholders(string(body))
		if len(idxs) == 0 {
			http.Error(w, "no placeholder", 400)
			return
		}
		capturedPlaceholder = string(body)[idxs[0][0]:idxs[0][1]]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"reply":"the key is %s right?"}`, capturedPlaceholder)
	}))
	defer upstream.Close()

	base, _, stop := startServer(t, upstream.URL)
	defer stop()

	body := mustJSON(t, map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "secret AKIAIOSFODNN7EXAMPLE here"},
		},
	})
	resp, err := http.Post(base+"/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(out), PlaceholderPrefix) {
		t.Errorf("placeholder leaked to client (not restored): %s", out)
	}
	if !strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("real value not restored on response: %s", out)
	}
}

func TestServer_StreamingSSE(t *testing.T) {
	// Upstream streams two SSE events, with the placeholder split
	// across the chunk boundary.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		idx := FindPlaceholders(string(body))
		if len(idx) == 0 {
			http.Error(w, "no placeholder", 400)
			return
		}
		ph := string(body)[idx[0][0]:idx[0][1]]
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		// First chunk: half of the placeholder.
		mid := len(ph) / 2
		fmt.Fprintf(w, "event: msg\ndata: pre %s", ph[:mid])
		fl.Flush()
		time.Sleep(10 * time.Millisecond)
		// Second chunk: rest + suffix.
		fmt.Fprintf(w, "%s post\n\n", ph[mid:])
		fl.Flush()
	}))
	defer upstream.Close()

	base, _, stop := startServer(t, upstream.URL)
	defer stop()

	body := []byte(`{"messages":[{"role":"user","content":"key=AKIAIOSFODNN7EXAMPLE"}]}`)
	resp, err := http.Post(base+"/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(out), PlaceholderPrefix) {
		t.Errorf("placeholder leaked across SSE chunks: %s", out)
	}
	if !strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("real value not restored in SSE response: %s", out)
	}
}

func TestServer_PassesNonJSONThrough(t *testing.T) {
	// e.g. /v1/audio/speech with multipart body — proxy should
	// forward unchanged.
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	base, _, stop := startServer(t, upstream.URL)
	defer stop()

	body := []byte("not-json sk-proj-abcdefghijklmnopqrstuvwxyz1234567890 trailing")
	resp, err := http.Post(base+"/v1/chat/completions", "text/plain", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if !bytes.Equal(got, body) {
		t.Errorf("non-json body modified by proxy:\n got %q\nwant %q", got, body)
	}
}

func TestServer_StatusEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	base, _, stop := startServer(t, upstream.URL)
	defer stop()
	resp, err := http.Get(base + "/__sanitizer/status")
	if err != nil {
		t.Fatalf("status get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("status body not JSON: %v\n%s", err, body)
	}
	if _, ok := parsed["uptime_seconds"]; !ok {
		t.Errorf("status missing uptime_seconds: %s", body)
	}
}

func TestServer_HealthEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	base, _, stop := startServer(t, upstream.URL)
	defer stop()
	resp, err := http.Get(base + "/__sanitizer/health")
	if err != nil {
		t.Fatalf("health get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health status: %d", resp.StatusCode)
	}
}

// TestServer_ContentLengthAfterRewrite reproduces the bug from PR
// #212's self-review: when the proxy sanitises a response that
// originally carried a Content-Length header, the placeholder→real
// expansion grows the body, but the un-touched Content-Length header
// would tell the client to stop reading too early. The fix is to
// strip Content-Length on rewritten responses; this test confirms
// strict clients read the whole body.
func TestServer_ContentLengthAfterRewrite(t *testing.T) {
	// 50+ char OpenAI key: longer than the placeholder string
	// (25 chars), so the restored body is LARGER than what the
	// upstream's Content-Length header said. A naive proxy that
	// forwards the Content-Length unchanged would cause the
	// client to truncate the body at the original byte count.
	const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGHIJKLMNOP"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		idx := FindPlaceholders(string(body))
		if len(idx) == 0 {
			http.Error(w, "no placeholder in upstream view", 400)
			return
		}
		ph := string(body)[idx[0][0]:idx[0][1]]
		respBody := fmt.Sprintf(`{"reply":"value is %s here"}`, ph)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(respBody)))
		w.WriteHeader(200)
		_, _ = w.Write([]byte(respBody))
	}))
	defer upstream.Close()
	base, _, stop := startServer(t, upstream.URL)
	defer stop()

	req := mustJSON(t, map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "use " + secret + " please"},
		},
	})
	resp, err := http.Post(base+"/v1/messages", "application/json", bytes.NewReader(req))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(out), secret) {
		t.Errorf("restored body truncated; got %q", out)
	}
	if strings.Contains(string(out), PlaceholderPrefix) {
		t.Errorf("placeholder leaked: %q", out)
	}
}

// TestServer_BinaryPassthrough — image/png response bodies must
// reach the client byte-for-byte. Running them through the
// StreamingReplacer would waste CPU and risk chunk-boundary
// buffering on realtime binary streams.
func TestServer_BinaryPassthrough(t *testing.T) {
	binary := make([]byte, 4096)
	for i := range binary {
		binary[i] = byte(i ^ 0xa5)
	}
	// Bury the placeholder-prefix substring inside the binary to
	// prove the bypass keys on Content-Type, not on content scan.
	copy(binary[1024:], []byte(PlaceholderPrefix+"099"+PlaceholderSuffix))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(binary)))
		w.WriteHeader(200)
		_, _ = w.Write(binary)
	}))
	defer upstream.Close()
	base, _, stop := startServer(t, upstream.URL)
	defer stop()
	resp, err := http.Get(base + "/v1/messages?stream=image")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, binary) {
		t.Errorf("binary body mutated (sizes: got %d, want %d)", len(got), len(binary))
	}
}

// TestServer_IgnoresHTTPProxyEnv covers bug #2 — the upstream
// client must not honour HTTP_PROXY / HTTPS_PROXY env vars (a
// hostile env var would tunnel sensitive bytes through an
// attacker before they reach the gateway). Point the env at a
// black-hole port; the proxy should still reach upstream because
// it ignores the env.
func TestServer_IgnoresHTTPProxyEnv(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	base, _, stop := startServer(t, upstream.URL)
	defer stop()
	resp, err := http.Get(base + "/v1/messages")
	if err != nil {
		t.Fatalf("request failed (proxy may be routing through env HTTPS_PROXY): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("upstream returned %d, want 204", resp.StatusCode)
	}
}

func TestRequireLoopback(t *testing.T) {
	cases := []struct {
		listen  string
		wantErr bool
	}{
		{"127.0.0.1:8888", false},
		{"127.0.0.1:0", false},
		{"[::1]:8888", false},
		{"localhost:8888", false},
		{"0.0.0.0:8888", true},
		{":8888", true},
		{"", true},
		{"192.168.1.50:8080", true},
		{"10.0.0.1:8888", true},
	}
	for _, c := range cases {
		err := requireLoopback(c.listen)
		if (err != nil) != c.wantErr {
			t.Errorf("requireLoopback(%q) err=%v, wantErr=%v", c.listen, err, c.wantErr)
		}
	}
}

func TestNew_NonLoopbackGatedByAllowFlag(t *testing.T) {
	base := Config{UpstreamBase: "https://example.com", Listen: "0.0.0.0:8888", Logger: log.New(io.Discard, "", 0)}
	if _, err := New(base); err == nil {
		t.Fatal("New should reject a 0.0.0.0 bind by default")
	}
	withFlag := base
	withFlag.AllowNonLoopback = true
	if _, err := New(withFlag); err != nil {
		t.Fatalf("New with AllowNonLoopback should accept 0.0.0.0: %v", err)
	}
}

func TestServer_GzipRequestBodySanitized(t *testing.T) {
	// A client that gzips its request body must NOT leak the secret:
	// the proxy has to inflate, scan, and forward an identity body.
	var gotBody []byte
	var gotEnc string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotEnc = r.Header.Get("Content-Encoding")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	base, _, stop := startServer(t, upstream.URL)
	defer stop()

	plain := `{"messages":[{"role":"user","content":"key sk-ant-foo_abcdefghijklmnopqrstuvwxyz1234 here"}]}`
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(plain))
	_ = zw.Close()

	req, _ := http.NewRequest("POST", base+"/v1/messages", bytes.NewReader(gz.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if bytes.Contains(gotBody, []byte("sk-ant-foo_abcdefghijklmnopqrstuvwxyz1234")) {
		t.Errorf("SECRET LEAKED upstream in gzip body: %s", gotBody)
	}
	if !bytes.Contains(gotBody, []byte(PlaceholderPrefix)) {
		t.Errorf("upstream didn't receive a sanitised placeholder body: %s", gotBody)
	}
	if gotEnc == "gzip" {
		t.Errorf("proxy forwarded a stale Content-Encoding: gzip with an identity body")
	}
	if !json.Valid(gotBody) {
		t.Errorf("upstream received a non-JSON (still-compressed?) body: %q", gotBody)
	}
}

func TestServer_UnsupportedRequestEncodingFailsClosed(t *testing.T) {
	// brotli isn't stdlib-decodable — the proxy must REFUSE rather
	// than forward an opaque body it couldn't scan.
	hit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	base, _, stop := startServer(t, upstream.URL)
	defer stop()

	req, _ := http.NewRequest("POST", base+"/v1/messages",
		strings.NewReader(`{"messages":[{"role":"user","content":"sk-ant-foo_abcdefghijklmnopqrstuvwxyz1234"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "br")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("want 415 fail-closed, got %d", resp.StatusCode)
	}
	if hit {
		t.Errorf("proxy forwarded an undecodable body upstream — secret could leak")
	}
}

func TestServer_GzipResponseRestored(t *testing.T) {
	// Upstream answers with a gzip-compressed body embedding the
	// placeholder. The proxy must decode it so the restorer runs and
	// the SDK sees the real secret, not a raw placeholder.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		idx := FindPlaceholders(string(body))
		if len(idx) == 0 {
			http.Error(w, "no placeholder", 400)
			return
		}
		ph := string(body)[idx[0][0]:idx[0][1]]
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(200)
		zw := gzip.NewWriter(w)
		fmt.Fprintf(zw, `{"reply":"the key is %s ok"}`, ph)
		_ = zw.Close()
	}))
	defer upstream.Close()
	base, _, stop := startServer(t, upstream.URL)
	defer stop()

	body := []byte(`{"messages":[{"role":"user","content":"secret AKIAIOSFODNN7EXAMPLE here"}]}`)
	resp, err := http.Post(base+"/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(out), PlaceholderPrefix) {
		t.Errorf("placeholder leaked to client (gzip response not decoded): %s", out)
	}
	if !strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("real value not restored from gzip response: %s", out)
	}
}

func TestServer_NonJSONOnSanitisablePathWarns(t *testing.T) {
	var logbuf bytes.Buffer
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	base, _, stop := startServerCfg(t, Config{
		UpstreamBase: upstream.URL,
		Logger:       log.New(&logbuf, "", 0),
	})
	defer stop()

	// multipart-ish, non-JSON body on a sanitisable path.
	resp, err := http.Post(base+"/v1/messages", "multipart/form-data",
		strings.NewReader("------x\r\nContent-Disposition: form-data\r\n\r\nsk-ant-leak_abcdefghijklmnopqrstuvwxyz\r\n------x--"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(logbuf.String(), "WARNING") || !strings.Contains(logbuf.String(), "non-JSON") {
		t.Errorf("expected a loud non-JSON warning, log was: %q", logbuf.String())
	}
}
