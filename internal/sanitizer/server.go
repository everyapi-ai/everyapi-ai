package sanitizer

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Config controls one running sanitizer proxy instance.
type Config struct {
	// Listen is the proxy's bind address. Default "127.0.0.1:8888".
	// MUST NOT be a public interface — the proxy holds plaintext
	// secrets in the mapping table and has no auth of its own.
	Listen string

	// UpstreamBase is the gateway endpoint, e.g.
	// "https://api.everyapi.ai". No trailing slash.
	UpstreamBase string

	// Detectors is the full sanitiser ruleset (built-ins + user
	// patterns). nil → use BuiltinDetectors().
	Detectors []Detector

	// Logger is used for non-fatal proxy events (connection errors,
	// detector statistics). nil → log.Default().
	Logger *log.Logger

	// RequestTimeout caps how long the proxy waits on the upstream
	// for a single non-streaming request. SSE responses ignore this
	// — they're driven by the upstream's own keepalives. Default 5
	// minutes.
	RequestTimeout time.Duration

	// ParentPID, when non-zero, ties the proxy's lifetime to that
	// pid. A background goroutine polls every 2s; once the pid is
	// no longer signallable (process has exited), the server
	// triggers its own context cancellation. This is how `everyapi
	// use <tool>` cleans up the detached proxy when the parent
	// tool process exits — spec §7-1 step 2's "父进程退出时清理".
	//
	// Setsid'd children would otherwise have ppid == 1 (init), so
	// we can't rely on getppid() detection. Explicit pid lets the
	// caller name a specific process to watch (typically `$$` of
	// the `everyapi use` invoker).
	ParentPID int

	// AllowNonLoopback permits binding Listen to a non-loopback
	// interface. Off by default: the proxy holds plaintext secrets in
	// the mapping table and has no auth of its own, so exposing it on
	// the LAN turns it into an open secret-relay. Only set this when
	// the operator has deliberately fronted it with their own
	// authenticated tunnel.
	AllowNonLoopback bool
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8888"
	}
	if c.UpstreamBase == "" {
		c.UpstreamBase = "https://api.everyapi.ai"
	}
	if c.Detectors == nil {
		c.Detectors = BuiltinDetectors()
	}
	if c.Logger == nil {
		c.Logger = log.Default()
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = 5 * time.Minute
	}
}

// Server hosts the running proxy. One instance per process; the CLI
// command layer (`everyapi proxy start`) constructs it and Run's blocks
// until Shutdown.
type Server struct {
	cfg     Config
	mapping *Mapping
	http    *http.Server
	upst    *url.URL
	// counters surfaced through /__sanitizer/status for `everyapi
	// proxy status`. Atomic so the status handler doesn't lock the
	// request hot path.
	stats stats
}

type stats struct {
	requests     atomic.Int64
	sanitised    atomic.Int64 // # requests that fired at least one detector
	bytesIn      atomic.Int64
	bytesOut     atomic.Int64
	startedAt    time.Time
	startedAtMu  sync.RWMutex
}

// New constructs a Server. Call Run to start it.
func New(cfg Config) (*Server, error) {
	cfg.applyDefaults()
	up, err := url.Parse(cfg.UpstreamBase)
	if err != nil {
		return nil, fmt.Errorf("parse upstream %q: %w", cfg.UpstreamBase, err)
	}
	if up.Scheme != "https" && up.Scheme != "http" {
		return nil, fmt.Errorf("upstream must be http or https, got %q", up.Scheme)
	}
	if !cfg.AllowNonLoopback {
		if err := requireLoopback(cfg.Listen); err != nil {
			return nil, err
		}
	}
	s := &Server{
		cfg:     cfg,
		mapping: NewMapping(),
		upst:    up,
	}
	s.stats.startedAtMu.Lock()
	s.stats.startedAt = time.Now()
	s.stats.startedAtMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/__sanitizer/status", s.handleStatus)
	mux.HandleFunc("/__sanitizer/health", s.handleHealth)
	mux.HandleFunc("/", s.handleProxy)

	s.http = &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}
	return s, nil
}

// Run binds the listener and blocks until ctx is canceled or the HTTP
// server errors. Returns nil on graceful shutdown, the underlying
// error otherwise.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.Listen, err)
	}
	// Wrap the caller's ctx so the parent-pid watcher can cancel
	// alongside SIGINT etc. without needing a separate channel.
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	if s.cfg.ParentPID > 0 {
		go s.watchParent(ctx, s.cfg.ParentPID, cancelRun)
	}
	// Cancel propagation: when ctx fires, gracefully shut down the
	// HTTP server. Serve returns http.ErrServerClosed on graceful
	// shutdown, which we treat as success.
	doneShutdown := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		doneShutdown <- s.http.Shutdown(shutCtx)
	}()
	s.cfg.Logger.Printf("sanitizer: listening on http://%s → %s", listener.Addr(), s.cfg.UpstreamBase)
	err = s.http.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if shutErr := <-doneShutdown; shutErr != nil && err == nil {
		err = shutErr
	}
	// Drop the mapping table on shutdown — secrets must not outlive
	// the process even by reference.
	s.mapping.Reset()
	return err
}

// Mapping exposes the live table for tests and `everyapi proxy status`.
func (s *Server) Mapping() *Mapping { return s.mapping }

// watchParent polls the given pid every 2s; if the process no
// longer responds to signal(0) — i.e. it's exited and been reaped —
// triggers cancel() so the server starts a graceful shutdown.
//
// Why polling instead of PR_SET_PDEATHSIG: a detached proxy is
// already in its own session (Setsid), so the kernel sees its
// parent as init (pid 1) regardless of what process actually
// spawned it. We can't rely on the kernel to notice the "real"
// parent dying. Explicit-pid polling is simple and portable.
//
// 2s is a deliberate trade-off: short enough that a sanitizer
// doesn't outlive its parent by more than a couple of seconds, long
// enough that the syscall overhead is negligible.
func (s *Server) watchParent(ctx context.Context, pid int, cancel context.CancelFunc) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !pidAlive(pid) {
				s.cfg.Logger.Printf("sanitizer: parent pid %d gone, shutting down", pid)
				cancel()
				return
			}
		}
	}
}

// pidAlive reports whether `pid` is signal-receivable by this
// process. Returns false on ESRCH / "no such process" — the parent
// is gone. Other errors (e.g. EPERM, which would mean the pid is
// alive but owned by another user) are conservatively treated as
// "alive" so we don't tear down a proxy whose parent we simply
// can't see clearly.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := osFindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	msg := err.Error()
	if strings.Contains(msg, "no such process") || strings.Contains(msg, "process already finished") {
		return false
	}
	return true
}

// osFindProcess is split out so tests can stub it. In production
// it's just os.FindProcess.
var osFindProcess = func(pid int) (interface {
	Signal(os.Signal) error
}, error) {
	return os.FindProcess(pid)
}

// handleHealth returns 200 OK with a short body. Used by `everyapi use`
// to detect "is the proxy already up?" before spawning a new one.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}

// handleStatus returns a small JSON payload describing the running
// proxy. Useful for `everyapi proxy status`.
func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.stats.startedAtMu.RLock()
	uptime := time.Since(s.stats.startedAt)
	s.stats.startedAtMu.RUnlock()
	body := fmt.Sprintf(
		`{"listen":%q,"upstream":%q,"uptime_seconds":%d,"requests":%d,"sanitised_requests":%d,"bytes_in":%d,"bytes_out":%d,"mapping_size":%d}`,
		s.cfg.Listen, s.cfg.UpstreamBase, int(uptime.Seconds()),
		s.stats.requests.Load(), s.stats.sanitised.Load(),
		s.stats.bytesIn.Load(), s.stats.bytesOut.Load(),
		s.mapping.Size(),
	)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// pickProtocol dispatches by request path. nil if no protocol claims
// the path — in that case the request still proxies through, but the
// body isn't parsed (no sanitisation possible without a schema).
func (s *Server) pickProtocol(path string) Protocol {
	for _, p := range Protocols() {
		if p.PathMatch(path) {
			return p
		}
	}
	return nil
}

// handleProxy is the request-rewrite + reverse-proxy entry point.
// Flow:
//
//  1. Read the request body fully (request bodies are small relative
//     to responses; buffering is fine).
//  2. Pick a protocol by path; if matched, run RewriteRequest.
//  3. Build an outbound *http.Request against the upstream base,
//     copying method, headers (minus hop-by-hop), and the rewritten body.
//  4. Stream the upstream response through StreamingReplacer back to
//     the client. The replacer handles both buffered JSON responses
//     and SSE streams identically — placeholders embedded in either
//     get restored on the way out.
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	s.stats.requests.Add(1)

	// Read body.
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		http.Error(w, "proxy: read request body: "+err.Error(), http.StatusBadGateway)
		return
	}
	s.stats.bytesIn.Add(int64(len(body)))

	// Decode a content-encoded request BEFORE scanning. The detector
	// only sees plaintext; a gzip/deflate body would sail past it and
	// the raw secret would be forwarded upstream. Fail CLOSED on an
	// encoding we can't decode — silently forwarding an opaque body
	// defeats the entire point of the proxy.
	reqEnc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	bodyWasEncoded := reqEnc != "" && reqEnc != "identity"
	if bodyWasEncoded && len(body) > 0 {
		decoded, derr := decodeBody(reqEnc, body)
		if derr != nil {
			http.Error(w,
				"proxy: refusing to forward a "+reqEnc+"-encoded request body that "+
					"can't be sanitised: "+derr.Error(),
				http.StatusUnsupportedMediaType)
			return
		}
		body = decoded
	}

	var rewritten []byte
	// proto is function-scoped (used again for the response
	// sanitise/Content-Length decisions below).
	proto := s.pickProtocol(r.URL.Path)
	if proto != nil && len(body) > 0 {
		// A sanitisable path whose body isn't JSON (multipart upload,
		// form-encoded, raw audio) flows through the protocol layer
		// untouched — the JSON walker has nothing to walk. That's a
		// silent privacy gap; make it loud so the user knows secrets
		// in such bodies are NOT protected.
		if !looksJSON(body) {
			s.cfg.Logger.Printf(
				"sanitizer: WARNING %s %s has a non-JSON body (%d bytes) — it is "+
					"forwarded WITHOUT sanitisation; secrets in multipart/form/binary "+
					"payloads are not detected",
				r.Method, r.URL.Path, len(body))
		}
		preMapSize := s.mapping.Size()
		rewritten, err = proto.RewriteRequest(body, s.cfg.Detectors, s.mapping)
		if err != nil {
			http.Error(w, "proxy: rewrite body: "+err.Error(), http.StatusBadGateway)
			return
		}
		if s.mapping.Size() > preMapSize {
			s.stats.sanitised.Add(1)
		}
	} else {
		rewritten = body
	}

	// Build outbound request.
	outURL := *s.upst
	outURL.Path = strings.TrimRight(s.upst.Path, "/") + r.URL.Path
	outURL.RawQuery = r.URL.RawQuery

	// Per-request deadline. Arm a timer that cancels the request after
	// RequestTimeout, but DISARM it once the response proves to be a
	// stream — SSE legitimately runs for minutes and must not be
	// guillotined by the non-streaming timeout (see Config doc). Client
	// disconnect / proxy shutdown still cancels via r.Context().
	reqCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var timer *time.Timer
	if _, hasDeadline := r.Context().Deadline(); !hasDeadline && s.cfg.RequestTimeout > 0 {
		timer = time.AfterFunc(s.cfg.RequestTimeout, cancel)
		defer timer.Stop()
	}

	// bytes.NewReader avoids the strings.NewReader(string(bytes))
	// double allocation and is seekable, so the transport sets
	// ContentLength automatically. reqCtx carries the streaming-aware
	// deadline armed above.
	outReq, err := http.NewRequestWithContext(reqCtx, r.Method, outURL.String(), bytes.NewReader(rewritten))
	if err != nil {
		http.Error(w, "proxy: build upstream request: "+err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(outReq.Header, r.Header)
	// Strip hop-by-hop headers per RFC 7230 §6.1.
	for _, h := range hopByHopHeaders {
		outReq.Header.Del(h)
	}
	// The forwarded body's length/encoding changed: it was decoded
	// above (bodyWasEncoded) and/or placeholder-rewritten (proto). The
	// wire uses outReq.ContentLength (set by bytes.NewReader); strip
	// the stale string Content-Length so observers see the truth, and
	// drop Content-Encoding because we always forward identity now.
	if proto != nil || bodyWasEncoded {
		outReq.Header.Del("Content-Length")
	}
	outReq.Header.Del("Content-Encoding")
	// Force an identity-decodable response: remove the SDK's
	// Accept-Encoding so the Go transport negotiates + transparently
	// decodes gzip itself, leaving the body as plaintext the
	// placeholder restorer can scan. Without this, a gzipped upstream
	// response reaches the SDK with raw <<__EVERYAPI_SECRET_NNN__>>
	// placeholders that never get restored.
	outReq.Header.Del("Accept-Encoding")
	// The upstream needs to know the request reaches it from the
	// proxy, not whatever the SDK set as Host.
	outReq.Host = s.upst.Host

	resp, err := upstreamClient.Do(outReq)
	if err != nil {
		http.Error(w, "proxy: upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// SSE ignores RequestTimeout — stop the timer so a long stream
	// isn't cut mid-flight.
	if timer != nil && isStreamingResponse(resp) {
		timer.Stop()
	}

	// Defence in depth: we forced an identity request, so the Go
	// transport transparently decodes a gzip response (and strips its
	// Content-Encoding/Length). If the upstream still returned an
	// encoding the transport didn't handle, decode it here so we never
	// emit un-restored placeholders or a stranded encoded stream.
	respBody := resp.Body
	decodedHere := false
	if ce := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); ce != "" && ce != "identity" {
		dec, derr := wrapDecoder(ce, resp.Body)
		if derr != nil {
			http.Error(w, "proxy: cannot decode "+ce+" upstream response: "+derr.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = dec.Close() }()
		respBody = dec
		decodedHere = true
	}

	// Binary / unknown-protocol responses bypass the placeholder
	// replacer (image/audio/etc.): forwarding them verbatim avoids the
	// per-chunk regex scan and any chance of binary that resembles our
	// placeholder prefix being mangled.
	sanitiseResponse := proto != nil && bodyLooksReplaceable(resp.Header.Get("Content-Type"))

	// Copy response headers, skipping hop-by-hop. Drop Content-Encoding
	// (we emit a decoded identity body; if absent this is a no-op).
	// Drop Content-Length when the replacer will grow the body, or we
	// decoded it ourselves (length changed); a verbatim binary
	// passthrough keeps its accurate length.
	for k, vs := range resp.Header {
		lk := strings.ToLower(k)
		if hopByHopSet[lk] || lk == "content-encoding" {
			continue
		}
		if lk == "content-length" && (sanitiseResponse || decodedHere) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	if !sanitiseResponse {
		// Binary / unknown-protocol response: forward bytes verbatim
		// without running them through the placeholder replacer.
		// Saves the per-chunk regex scan + carryover bookkeeping
		// that would otherwise add latency to image / audio streams,
		// and avoids any chance of the replacer being confused by
		// binary that happens to contain bytes resembling our
		// placeholder prefix.
		n, _ := io.Copy(w, respBody)
		s.stats.bytesOut.Add(n)
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// Stream the response body through the placeholder replacer.
	replacer := NewStreamingReplacer(s.mapping)
	buf := make([]byte, 16*1024)
	for {
		n, readErr := respBody.Read(buf)
		if n > 0 {
			out := replacer.Write(buf[:n])
			if len(out) > 0 {
				if _, werr := w.Write(out); werr != nil {
					return
				}
				s.stats.bytesOut.Add(int64(len(out)))
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	if out := replacer.Final(); len(out) > 0 {
		_, _ = w.Write(out)
		s.stats.bytesOut.Add(int64(len(out)))
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// bodyLooksReplaceable reports whether a Content-Type implies the
// response body could contain text we'd want to placeholder-restore.
// JSON and text/event-stream are the two shapes the protocols return;
// everything else (binary images, audio, multipart) bypasses the
// replacer entirely. An empty Content-Type defaults to "yes" — the
// upstream may have omitted it and we'd rather scan harmless text
// than miss a JSON body that forgot its header.
func bodyLooksReplaceable(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if ct == "" {
		return true
	}
	// Strip parameters ("application/json; charset=utf-8" → "application/json").
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case strings.HasPrefix(ct, "application/json"):
		return true
	case ct == "text/event-stream":
		return true
	case strings.HasPrefix(ct, "text/"):
		// text/plain, text/html, text/csv — still text, still scannable.
		return true
	}
	return false
}

// upstreamClient is the shared HTTP client for outbound calls. Long
// timeouts (responses can stream for minutes); no per-request retry
// (the SDK on the buyer side handles retries with the proper
// semantics for its own protocol).
//
// Proxy is explicitly nil. The default http.ProxyFromEnvironment
// would let HTTP_PROXY / HTTPS_PROXY env vars route the sanitiser's
// outbound traffic through whatever third party they point at —
// which undermines the §7-2 trust-minimal guarantee (a hostile env
// var in the buyer's rc file would tunnel still-sensitive bytes
// through an attacker before they reach the real gateway). The proxy
// always connects to the upstream directly; if a buyer genuinely
// needs an HTTP_PROXY for their network egress, they can configure
// the gateway URL itself accordingly.
var upstreamClient = &http.Client{
	Timeout: 0, // governed by per-request context
	Transport: &http.Transport{
		Proxy:                 nil,
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// hopByHopHeaders / hopByHopSet — RFC 7230 §6.1 lists the headers
// that must not be forwarded by a proxy. Keep both forms (slice for
// iteration when deleting from a Header, set for case-insensitive
// "should I skip this" check on responses).
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

var hopByHopSet = func() map[string]bool {
	m := make(map[string]bool, len(hopByHopHeaders))
	for _, h := range hopByHopHeaders {
		m[strings.ToLower(h)] = true
	}
	return m
}()

// requireLoopback rejects a Listen address that isn't bound to a
// loopback interface. An empty host ("" / ":8888") means "all
// interfaces" and is rejected too. Bypass via Config.AllowNonLoopback
// only when the operator has deliberately fronted the proxy with their
// own auth.
func requireLoopback(listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		// Maybe it's a bare host with no port — still has to be loopback.
		host = listen
	}
	if host == "" {
		return fmt.Errorf("refusing to bind %q: empty host binds all interfaces; "+
			"the sanitizer holds plaintext secrets and has no auth — bind 127.0.0.1 "+
			"or set AllowNonLoopback if it's behind your own authenticated front", listen)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("refusing to bind %q: host %q is not an IP and not "+
			"\"localhost\" — can't prove it's loopback; set AllowNonLoopback to override", listen, host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("refusing to bind %q: %s is not a loopback address; "+
			"the sanitizer holds plaintext secrets and has no auth of its own — "+
			"set AllowNonLoopback only if it's behind your own authenticated front", listen, host)
	}
	return nil
}

// maxDecodedBody caps how many bytes we'll inflate from a single
// content-encoded body. A hostile or buggy client could ship a small
// gzip "bomb" that expands to gigabytes; the proxy runs on the user's
// own machine but should still not OOM. 64 MiB comfortably exceeds any
// real LLM request payload.
const maxDecodedBody = 64 << 20

// looksJSON reports whether body is plausibly a JSON object/array —
// the only shape the protocol walkers can sanitise. Used to warn when
// a sanitisable path carries a non-JSON (multipart/binary) body.
func looksJSON(body []byte) bool {
	t := bytes.TrimSpace(body)
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}

// isStreamingResponse reports whether resp is a server-sent-event
// stream. SSE is the long-lived case Config.RequestTimeout explicitly
// exempts.
func isStreamingResponse(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "event-stream")
}

// decodeBody fully inflates a content-encoded request body. Returns an
// error for any encoding we can't decode so the caller can fail closed
// instead of forwarding an unsanitisable opaque body.
func decodeBody(encoding string, data []byte) ([]byte, error) {
	rc, err := wrapDecoder(encoding, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	out, err := io.ReadAll(io.LimitReader(rc, maxDecodedBody+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxDecodedBody {
		return nil, fmt.Errorf("decoded body exceeds %d bytes — refusing", maxDecodedBody)
	}
	return out, nil
}

// wrapDecoder returns a streaming decoder for the given Content-Encoding.
// "deflate" is ambiguous in the wild (RFC 7230 says zlib-wrapped, many
// servers send raw); we try zlib and fall back to raw flate.
func wrapDecoder(encoding string, r io.Reader) (io.ReadCloser, error) {
	switch encoding {
	case "gzip", "x-gzip":
		return gzip.NewReader(r)
	case "deflate":
		buf := bufio.NewReader(r)
		if zr, err := zlib.NewReader(buf); err == nil {
			return zr, nil
		}
		return flate.NewReader(buf), nil
	default:
		return nil, fmt.Errorf("unsupported content-encoding %q", encoding)
	}
}

// copyHeaders moves header values from src to dst, preserving the
// multi-value semantics of Add.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
