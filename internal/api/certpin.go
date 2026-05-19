// Report-only TLS public-key pinning for the official EveryAPI hosts
// (EVERYAPI §7-5 Layer 2). Deliberately conservative first cut: we NEVER
// reject a connection.
//
// Behaviour on a TLS handshake to an official `*.everyapi.ai` host:
//
//   - expectedSPKIPins EMPTY: the hook is a SILENT no-op (the dormant
//     state this shipped in originally — kept as the safe default if
//     the set is ever cleared).
//   - expectedSPKIPins POPULATED (NOW — real leaf pins are filled in):
//     silent when the leaf SPKI pin is in the set; warn once per
//     host+pin on mismatch (still allowing the connection — a
//     corporate/ISP TLS proxy is a common benign cause).
//     `pinReporter.enforce` (reserved) flips that warning into a hard
//     rejection in a later change; it is still false here.
//
// While enforce is false every code path returns nil to crypto/tls.
//
// Scope is strictly the official hosts. Self-host / dev / custom
// `--api-base` (localhost, private IPs, other domains) are never
// inspected — pinning a user's own gateway makes no sense and would
// just be noise. `EVERYAPI_TLS_PIN=off` disables the hook entirely.

package api

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// expectedSPKIPins is the curated set of base64(SHA-256(SPKI)) values
// the official hosts are expected to present.
//
// Only api.everyapi.ai is pinned: that is the ONLY host the CLI's
// pinned transport actually TLS-dials (the sanitizer proxy forwards
// there too). app.everyapi.ai / everyapi.ai are opened in the user's
// browser via cliprompt.OpenBrowser(), not through this http.Client, so a pin
// for them would never be exercised by inspect() — pinning only what
// is actually verified keeps the set honest and avoids shipping
// config no smoke test can reach.
//
// LIVE LEAF pin captured 2026-05-19 (Fly/Let's Encrypt leaf). This is
// the LEAF, not the issuing intermediate, so it WILL go stale when
// Let's Encrypt rotates (~60–90 days). Acceptable because pinning is
// report-only (a stale pin only produces a one-line stderr warning,
// never a broken connection) and rotation is scheduled maintenance:
//
// ── CERT-ROTATION RUNBOOK ──────────────────────────────────────────
//  1. Re-capture the live leaf pin:
//     openssl s_client -servername api.everyapi.ai \
//     -connect api.everyapi.ai:443 </dev/null 2>/dev/null \
//     | openssl x509 -noout -pubkey | openssl pkey -pubin \
//     -outform der 2>/dev/null | openssl dgst -sha256 -binary \
//     | openssl base64
//  2. Replace the value below; bump the capture date in this comment.
//  3. Ship a CLI release. Until users upgrade, an old binary just logs
//     the (benign) mismatch — no functional breakage.
//
// NOTE: inspect() only computes the LEAF cert's SPKI and checks
// membership here — it does not walk the chain. So simply adding an
// issuing-intermediate pin to this map would be inert today. Making
// the CLI survive a leaf rotation WITHOUT a release requires BOTH
// adding the intermediate pin here AND changing inspect() to also
// accept a match on any chain cert's SPKI. That is a deliberate
// follow-up, tracked for the enforce PR; flagged here so it isn't
// lost.
// ───────────────────────────────────────────────────────────────────
var expectedSPKIPins = map[string]struct{}{
	"8ero6X8jXA0TU+kjHCqNRfOHlpO6N8K/CC2bvxCGQ1g=": {}, // api.everyapi.ai (Fly/Let's Encrypt leaf)
}

// isOfficialEveryAPIHost reports whether host is an official EveryAPI
// endpoint that should be pin-inspected. Exact `everyapi.ai` or any
// `*.everyapi.ai` subdomain — and nothing else. The leading-dot suffix
// check is deliberate so look-alikes do NOT match: `everyapipro.com`,
// `noteveryapi.ai`, `xeveryapi.ai`, `everyapi.ai.evil.com` all fail.
func isOfficialEveryAPIHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	// SNI carries no port, but be defensive if a caller passes one.
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	// A rooted FQDN ("everyapi.ai.") is the same host; strip the dot so
	// it still matches rather than silently skipping the check.
	host = strings.TrimSuffix(host, ".")
	return host == "everyapi.ai" || strings.HasSuffix(host, ".everyapi.ai")
}

// spkiPin returns the standard HPKP-style pin for a certificate:
// base64( SHA-256( DER SubjectPublicKeyInfo ) ). cert.RawSubjectPublicKeyInfo
// is exactly that DER, so no re-marshal (which could canonicalise
// differently) is needed.
func spkiPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// pinReporter holds the report-only state. A struct (rather than bare
// package funcs) so tests can drive it with an in-memory sink and a
// seeded expected set instead of poking globals / capturing os.Stderr.
type pinReporter struct {
	out      io.Writer
	expected map[string]struct{}
	disabled bool
	enforce  bool // reserved: a later change flips this to reject

	mu   sync.Mutex
	seen map[string]struct{} // host|pin already reported this process
}

func newPinReporter() *pinReporter {
	return &pinReporter{
		out:      os.Stderr,
		expected: expectedSPKIPins,
		disabled: strings.EqualFold(strings.TrimSpace(os.Getenv("EVERYAPI_TLS_PIN")), "off"),
		seen:     make(map[string]struct{}),
	}
}

// inspect looks at one verified connection. It is called from
// tls.Config.VerifyConnection, which runs AFTER normal certificate
// verification — so this only ever ADDS a report, it does not relax
// or replace chain validation. It returns nil unconditionally while
// enforce is false.
func (p *pinReporter) inspect(serverName string, leaf *x509.Certificate) error {
	if p == nil || p.disabled || leaf == nil {
		return nil
	}
	if !isOfficialEveryAPIHost(serverName) {
		return nil
	}
	if len(p.expected) == 0 {
		// Dormant: no baseline configured yet, so there is nothing to
		// compare against. Stay completely silent — emitting the
		// observed pin on every run would be unactionable noise and is
		// not actually "collected" anywhere.
		return nil
	}

	pin := spkiPin(leaf)
	if _, ok := p.expected[pin]; ok {
		return nil
	}

	// Mismatch. Loud, but still report-only: the connection is
	// allowed. A later change returns an error here when enforce.
	p.logOnce(serverName, pin, fmt.Sprintf(
		"⚠ everyapi: TLS public-key pin MISMATCH for %s (got sha256/%s). "+
			"This can mean a corporate/ISP TLS proxy, or an attack. "+
			"Connection ALLOWED (pinning is report-only). "+
			"Set EVERYAPI_TLS_PIN=off to silence.", serverName, pin))
	if p.enforce {
		return fmt.Errorf("everyapi: TLS pin mismatch for %s", serverName)
	}
	return nil
}

// PinMismatchHook is an optional callback that fires once per
// host+pin per process whenever the TLS pin mismatches. Useful for
// the menubar, which has no stderr surface a user would ever read;
// it can register a hook that pops a desktop notification. Nil =
// no callback (the default — preserves the CLI's stderr-only
// behaviour). Set before the first HTTP request.
var PinMismatchHook func(host, pin, msg string)

// logOnce writes msg to p.out at most once per host+pin per process,
// so normal repeated requests don't spam stderr, and also fires
// PinMismatchHook so GUI surfaces can render their own warning.
func (p *pinReporter) logOnce(host, pin, msg string) {
	key := host + "|" + pin
	p.mu.Lock()
	if _, done := p.seen[key]; done {
		p.mu.Unlock()
		return
	}
	p.seen[key] = struct{}{}
	p.mu.Unlock()
	fmt.Fprintln(p.out, msg)
	if PinMismatchHook != nil {
		PinMismatchHook(host, pin, msg)
	}
}

var (
	defaultPinReporter     *pinReporter
	defaultPinReporterOnce sync.Once
	pinnedTransport        *http.Transport
	pinnedTransportOnce    sync.Once
)

func getPinReporter() *pinReporter {
	defaultPinReporterOnce.Do(func() { defaultPinReporter = newPinReporter() })
	return defaultPinReporter
}

// pinReportingTransport clones http.DefaultTransport (preserving env
// proxy support, HTTP/2, timeouts) and attaches the report-only
// VerifyConnection hook. Cloning matters: users behind a corporate
// proxy must still reach EveryAPI — we only want to LOG the resulting
// pin mismatch, never break them.
func pinReportingTransport() *http.Transport {
	pinnedTransportOnce.Do(func() {
		base, _ := http.DefaultTransport.(*http.Transport)
		tr := base.Clone()
		cfg := tr.TLSClientConfig
		if cfg == nil {
			cfg = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			var leaf *x509.Certificate
			if len(cs.PeerCertificates) > 0 {
				leaf = cs.PeerCertificates[0]
			}
			return getPinReporter().inspect(cs.ServerName, leaf)
		}
		tr.TLSClientConfig = cfg
		pinnedTransport = tr
	})
	return pinnedTransport
}
