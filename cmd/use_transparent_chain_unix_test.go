//go:build !windows

package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

const (
	useChainCallerEnv = "EVERYAPI_TEST_USE_CHAIN_CALLER"
	useChainShimEnv   = "EVERYAPI_TEST_USE_CHAIN_SHIM"
	awsKeyFixture     = "AKIAIOSFODNN7EXAMPLE"
)

// TestUseWiresTheSanitizerAsTheConnectorUpstream pins the one line that makes
// --sanitize compose with transparent mode: connectorUpstream = proxyAddr in
// Use. Nothing else covers it.
// TestTransparentConnectorChainsThroughRecoveryGuard looks like it does, but it
// hands startTransparentConnector the sanitizer address itself and never calls
// Use — so it proves the chain works once wired, not that Use wires it.
// Reverting that assignment leaves it green.
//
// Masking is the evidence: the sanitizer is the only component that rewrites a
// request body, so an AWS key that reaches the gateway redacted can only have
// travelled child -> connector -> sanitizer -> gateway.
func TestUseWiresTheSanitizerAsTheConnectorUpstream(t *testing.T) {
	switch {
	case os.Getenv(useChainShimEnv) == "1":
		runChainShim(t)
		return
	case os.Getenv(useChainCallerEnv) == "1":
		if err := Use([]string{"claude", "--sanitize"}); err != nil {
			t.Fatal(err)
		}
		return
	}

	var mu sync.Mutex
	var relayBodies []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			relayBodies = append(relayBodies, string(body))
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "claude-test", "owned_by": "anthropic", "supported_endpoint_types": []string{"anthropic"}}}})
	}))
	defer gateway.Close()

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	if err := config.Save(&config.Credentials{APIBase: gateway.URL, RelayKey: "sk-everyapi-test"}); err != nil {
		t.Fatal(err)
	}

	shimDir := t.TempDir()
	shim := "#!/bin/sh\n" + useChainShimEnv + "=1 exec \"$EVERYAPI_TEST_USE_TEST_BINARY\" -test.run=^TestUseWiresTheSanitizerAsTheConnectorUpstream$\n"
	if err := os.WriteFile(filepath.Join(shimDir, "claude"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}

	hostEnv := []string{}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
			"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy":
			continue
		}
		hostEnv = append(hostEnv, kv)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestUseWiresTheSanitizerAsTheConnectorUpstream$")
	child.Env = append(hostEnv,
		useChainCallerEnv+"=1",
		"EVERYAPI_TEST_USE_TEST_BINARY="+os.Args[0],
		"XDG_CONFIG_HOME="+configRoot,
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("use --sanitize failed: %v\n%s", err, out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(relayBodies) == 0 {
		t.Fatal("no relayed request reached the gateway; the chain never carried the child's traffic")
	}
	for _, b := range relayBodies {
		if strings.Contains(b, awsKeyFixture) {
			t.Fatalf("the AWS key reached the gateway unmasked — the connector relayed straight past the sanitizer, so --sanitize is silently a no-op under transparent mode: %s", b)
		}
	}
}

// runChainShim stands in for `claude`: it talks to the connector exactly as the
// real tool would — through HTTPS_PROXY, trusting NODE_EXTRA_CA_CERTS — and
// sends a body carrying a secret the sanitizer is expected to mask.
func runChainShim(t *testing.T) {
	t.Helper()
	proxy := os.Getenv("HTTPS_PROXY")
	caPath := os.Getenv("NODE_EXTRA_CA_CERTS")
	if proxy == "" || caPath == "" {
		t.Fatalf("child env missing transparent wiring: HTTPS_PROXY=%q NODE_EXTRA_CA_CERTS=%q", proxy, caPath)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("connector CA bundle contains no certificate")
	}
	proxyURL, err := url.Parse(proxy)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
	body := `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"my key is ` + awsKeyFixture + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through connector: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
