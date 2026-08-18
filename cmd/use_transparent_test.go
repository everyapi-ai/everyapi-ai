package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/everyapi-ai/everyapi-sdk/connector"
)

func TestStartTransparentConnectorRelaysAndCleansUp(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	capturedAuth := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: done\n\n")
	}))
	defer upstream.Close()

	session, err := startTransparentConnector(upstream.URL, upstream.URL, "real-relay-key")
	if err != nil {
		t.Fatalf("startTransparentConnector: %v", err)
	}
	caPath := session.caPath
	if !strings.HasPrefix(session.proxyURL, "http://127.0.0.1:") {
		t.Errorf("proxy URL = %q, want IPv4 loopback", session.proxyURL)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(caPEM), "real-relay-key") {
		t.Fatal("CA bundle contains relay credential")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(caPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("CA bundle permissions = %o, want 0600", got)
		}
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("connector CA bundle contains no certificate")
	}
	proxy, _ := url.Parse(session.proxyURL)
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxy),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
	req, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer client-placeholder")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through connector: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if got := <-capturedAuth; got != "Bearer real-relay-key" {
		t.Errorf("gateway Authorization = %q", got)
	}

	session.stop()
	session.stop() // cleanup is intentionally idempotent
	if _, err := os.Stat(caPath); !os.IsNotExist(err) {
		t.Errorf("CA bundle still exists after stop: %v", err)
	}
	if _, err := client.Do(req.Clone(req.Context())); err == nil {
		t.Fatal("request unexpectedly succeeded after connector stop")
	}
}

func TestStartTransparentConnectorRejectsMissingRelayKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := startTransparentConnector("https://api.everyapi.ai", "https://api.everyapi.ai", ""); err == nil {
		t.Fatal("missing relay key unexpectedly accepted")
	}
}

func TestSweepStaleConnectorCABundles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	connectorDir := filepath.Join(root, "everyapi", "connector")
	if err := os.MkdirAll(connectorDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// A fresh bundle (a concurrently-launching session's CA) and an orphan past the reap floor (a SIGKILLed session's leftover).
	fresh := filepath.Join(connectorDir, "ca-fresh.pem")
	orphan := filepath.Join(connectorDir, "ca-orphan.pem")
	for _, p := range []string{fresh, orphan} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Derived from connector.CertificateLifetime, NOT from staleConnectorCAAge: keying the fixture off the very constant under test made this pass for any value of it, including the 24h floor that deleted live sessions' CA bundles. The property that matters is that a bundle older than the CA's own validity is reaped, so express that directly.
	old := time.Now().Add(-connector.CertificateLifetime - 48*time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	sweepStaleConnectorCABundles()

	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh CA bundle was swept (a live session's bundle must survive): %v", err)
	}
	// The floor must exceed the CA's own validity, or a session that outlives its certificate has its in-use bundle deleted out from under it.
	if staleConnectorCAAge <= connector.CertificateLifetime {
		t.Errorf("staleConnectorCAAge = %v, must exceed connector.CertificateLifetime (%v)",
			staleConnectorCAAge, connector.CertificateLifetime)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("stale CA bundle survived the sweep: stat err = %v", err)
	}
}

func TestStartTransparentLaunchDoesNotExposeRelayKeyOrGateway(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tool, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	launch, err := startTransparentLaunch(tool, "https://api.everyapi.ai", "https://api.everyapi.ai", "real-relay-key")
	if err != nil {
		t.Fatal(err)
	}
	defer launch.session.stop()
	for key, value := range launch.env {
		if strings.Contains(value, "real-relay-key") || strings.Contains(value, "api.everyapi.ai") {
			t.Errorf("child env %s leaks private routing value %q", key, value)
		}
	}
	if got := launch.env["ANTHROPIC_BASE_URL"]; got != "https://api.anthropic.com" {
		t.Errorf("ANTHROPIC_BASE_URL = %q, want official origin for connector-backed model discovery", got)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "NO_PROXY", "no_proxy"} {
		found := false
		for _, unset := range launch.unsetEnv {
			if unset == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("child unset list missing %s", key)
		}
	}
}

// TestTransparentConnectorChainsThroughRecoveryGuard is the end-to-end proof for the chain that replaced the old "--transparent cannot launch a session that needs the recovery guard" hard error: with the sanitizer as the connector's upstream, a tool that stays on the vendor's official origin still gets the Claude recovery guard applied to its relayed traffic.
//
//	child -> connector (api.anthropic.com MITM) -> sanitizer (guard) -> gateway
//
// The gateway here streams the known-polluted assistant text; the guard must drop it before it can reach the child, exactly as it does on the injected path (see TestUseExecReceivesRecoveredClaudeSessionID).
func TestTransparentConnectorChainsThroughRecoveryGuard(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Guarded: written from the httptest handler goroutine, read from the test goroutine after the response lands.
	var authMu sync.Mutex
	var gatewayAuth string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
			authMu.Lock()
			gatewayAuth = r.Header.Get("Authorization")
			authMu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"course\\ncourse\\ncourse\"}}\n\n"))
			_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer gateway.Close()

	// Guard on, masking off — the recovery-guard configuration.
	sanitizerAddr, stopSanitizer, err := startInProcessSanitizer(gateway.URL, false, true, nil)
	if err != nil {
		t.Fatalf("startInProcessSanitizer: %v", err)
	}
	defer stopSanitizer()

	// The connector relays THROUGH the sanitizer rather than straight at the gateway; that substitution is the whole fix.
	session, err := startTransparentConnector(sanitizerAddr, gateway.URL, "real-relay-key")
	if err != nil {
		t.Fatalf("startTransparentConnector: %v", err)
	}
	defer session.stop()

	caPEM, err := os.ReadFile(session.caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("connector CA bundle contains no certificate")
	}
	proxy, _ := url.Parse(session.proxyURL)
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxy),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}}

	req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages",
		strings.NewReader(`{"model":"claude-opus-4-8","stream":true,"messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through connector+sanitizer chain: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "course") {
		t.Fatalf("recovery guard did not run over the transparent chain — polluted text reached the child: %s", body)
	}
	// The chain must still swap the client's credential for the relay key on the way out, i.e. chaining did not bypass the connector's relay rewrite.
	authMu.Lock()
	gotAuth := gatewayAuth
	authMu.Unlock()
	if gotAuth != "Bearer real-relay-key" {
		t.Fatalf("gateway Authorization = %q, want the relay key injected by the connector", gotAuth)
	}
}

// TestTransparentDefaultResolution pins the default-on policy per tool, which is the contract the whole flip rests on:
//
//   - a tool with an adapter defaults to transparent;
//   - tools without one (Hermes is EveryAPI-native; Antigravity and LibreFang keep their native authentication/router paths) stay direct;
//   - an explicit --transparent on such a tool still fails loudly, because the user asked for something that cannot be delivered.
//
// The resolution itself lives inline in Use; this asserts the two inputs it reads (the parser's tri-state and Tool.SupportsTransparent) agree per tool.
func TestTransparentDefaultResolution(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		tool, err := tools.Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%s): %v", name, err)
		}
		if !tool.SupportsTransparent() {
			t.Errorf("%s must support transparent — it is in the default-on set", name)
		}
	}
	for _, name := range []string{"gemini", "openhands", "forge", "llxprt", "hermes"} {
		tool, err := tools.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		if tool.SupportsTransparent() {
			t.Errorf("%s must NOT claim transparent support", name)
		}
	}
}

// TestUseRejectsExplicitTransparentForUnsupportedTool guards the loud half of the policy above: silence is only for the unset default, never for an explicit request that cannot be honored.
func TestUseRejectsExplicitTransparentForUnsupportedTool(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{
		APIBase:  "https://api.everyapi.ai",
		RelayKey: "sk-everyapi-test",
	}); err != nil {
		t.Fatal(err)
	}
	err := Use([]string{"hermes", "--transparent"})
	if err == nil {
		t.Fatal("explicit --transparent on hermes unexpectedly accepted")
	}
	if !strings.Contains(err.Error(), "transparent mode is not supported") {
		t.Fatalf("err = %v, want the unsupported-tool message", err)
	}
}

// TestStartTransparentConnectorGuardsChainedInterceptedDestination pins the loop guard against the hole the sanitizer chain opened. connector.New refuses to relay to an intercepted official origin — that is what stops it from MITM-ing a host and handing the relay key straight back to that same host. The guard used to inspect the connector's immediate upstream, which was always the gateway; once the sanitizer became the upstream it inspected 127.0.0.1 instead, so the check silently passed for every chained launch and the safety of `everyapi use claude` depended on whether --sanitize happened to be set. The guard must follow the ULTIMATE destination.
func TestStartTransparentConnectorGuardsChainedInterceptedDestination(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// A loopback hop (as the sanitizer would be) in front of a destination that IS an intercepted official origin.
	_, err := startTransparentConnector("http://127.0.0.1:1", "https://api.anthropic.com", "real-relay-key")
	if err == nil {
		t.Fatal("chained launch to an intercepted official origin was accepted — the relay key would be handed to the vendor the connector is shielding")
	}
	if !strings.Contains(err.Error(), "intercepted official origin") {
		t.Fatalf("err = %v, want the intercepted-origin refusal", err)
	}

	// The unchained form of the same destination must stay refused too.
	if _, err := startTransparentConnector("https://api.anthropic.com", "", "real-relay-key"); err == nil {
		t.Fatal("direct launch to an intercepted official origin was accepted")
	}
}

// TestAllProxyOnlyEgressVar pins which proxy environments transparent mode must decline. All transparent-mode traffic is https (the relay leg, the CONNECT tunnel, and the injected-path gateway are all https://), and proxy resolution is per-scheme on every side, so only HTTPS_PROXY rescues a launch and only a lone ALL_PROXY strands one.
//
// An earlier version of this function — and this test — got two things wrong. First, it refused whenever HTTPS_PROXY was socks5, on the premise that the connector could not speak socks; that premise is false (net/http dials socks5 natively, verified with a real SOCKS5 server), and the downgrade wrote the real relay key into the child env. Second, it treated HTTP_PROXY as a proxy "the connector reads": it does not — HTTP_PROXY applies only to http targets, of which transparent mode has none — so an HTTP_PROXY set beside an ALL_PROXY wrongly short-circuited the ALL_PROXY fallback and hung a user who had a working catch-all proxy.
func TestAllProxyOnlyEgressVar(t *testing.T) {
	all := []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"}

	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no proxy at all", nil, ""},
		{"http HTTPS_PROXY is usable", map[string]string{"HTTPS_PROXY": "http://corp:8080"}, ""},
		// net/http dials socks5 proxy URLs natively, so the relay leg works and transparent must NOT be declined. Only pass-through CONNECT degrades.
		{"socks HTTPS_PROXY is usable — net/http speaks socks5", map[string]string{"HTTPS_PROXY": "socks5://127.0.0.1:1080"}, ""},
		{"lowercase socks https_proxy is usable", map[string]string{"https_proxy": "socks5h://127.0.0.1:1080"}, ""},
		// HTTP_PROXY is inert for the connector's https legs, so a lone HTTP_PROXY stays transparent: the common non-firewalled launch works and the relay key never reaches the child. Reporting it instead would divert every such launch onto the injected path and leak the key for no gain. (The narrow firewalled + HTTP_PROXY-only case can't reach the gateway either way — the user should set HTTPS_PROXY — so it's not worth a key-leaking fallback.)
		{"HTTP_PROXY alone stays transparent — inert for an https dial", map[string]string{"HTTP_PROXY": "http://corp:8080"}, ""},
		// ALL_PROXY alone: nobody reads it. Scheme does not matter.
		{"socks ALL_PROXY alone strands", map[string]string{"ALL_PROXY": "socks5://127.0.0.1:1080"}, "ALL_PROXY"},
		{"http ALL_PROXY alone strands too", map[string]string{"ALL_PROXY": "http://corp:8080"}, "ALL_PROXY"},
		{"lowercase all_proxy alone strands", map[string]string{"all_proxy": "socks5://127.0.0.1:1080"}, "all_proxy"},
		// A proxy the connector reads rescues it, whatever ALL_PROXY says.
		{"ALL_PROXY beside HTTPS_PROXY is fine", map[string]string{
			"ALL_PROXY": "socks5://127.0.0.1:1080", "HTTPS_PROXY": "http://corp:8080",
		}, ""},
		// An irrelevant HTTP_PROXY must NOT suppress the ALL_PROXY fallback: it contributes nothing to an https dial, so this must behave exactly like "ALL_PROXY alone" and report ALL_PROXY. Pinning the old "" here hung a user whose catch-all proxy would have worked on the injected path.
		{"ALL_PROXY beside an irrelevant HTTP_PROXY still strands", map[string]string{
			"ALL_PROXY": "socks5://127.0.0.1:1080", "HTTP_PROXY": "http://corp:8080",
		}, "ALL_PROXY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range all {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := allProxyOnlyEgressVar(); got != tc.want {
				t.Errorf("allProxyOnlyEgressVar() = %q, want %q", got, tc.want)
			}
		})
	}
}
