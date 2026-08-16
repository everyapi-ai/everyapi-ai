//go:build !windows

package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
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

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const (
	useChainCallerEnv = "EVERYAPI_TEST_USE_CHAIN_CALLER"
	useChainShimEnv   = "EVERYAPI_TEST_USE_CHAIN_SHIM"
	awsKeyFixture     = "AKIAIOSFODNN7EXAMPLE"
	// A non-claude id, so the catalogue transform has to alias it.
	realAliasedModel = "glm-4.7"
)

// TestUseWiresTheSanitizerAsTheConnectorUpstream pins the wiring that makes --sanitize compose with transparent mode, and — since the transforms were flattened onto one socket — that BOTH transforms compose there. TestTransparentConnectorChainsThroughRecoveryGuard looks like it covers the first part, but it hands startTransparentConnector the sanitizer address itself and never calls Use, so it proves the chain works once wired, not that Use wires it. Reverting the wiring leaves it green.
//
// One request carries the evidence for both transforms. The child discovers the catalogue the way a real client does (GET /v1/models), picks the synthetic alias it finds there, and posts to it with an AWS key in the body. Reaching the gateway as the REAL model id can only be the catalogue transform; reaching it with the key redacted can only be the sanitizer. Both effects present while connector.log records exactly three hops means one socket ran both — chain them again and that count goes to four.
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
		// glm-4.7 is the load-bearing entry: not being a claude-* id, the catalogue transform must republish it under a synthetic alias, which is what lets the child prove the transform ran.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{
			map[string]any{"id": "claude-test", "owned_by": "anthropic", "supported_endpoint_types": []string{"anthropic"}},
			map[string]any{"id": realAliasedModel, "owned_by": "zhipu", "supported_endpoint_types": []string{"anthropic"}},
		}})
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
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("use --sanitize failed: %v\n%s", err, out)
	}
	assertLaunchLineReportsOnlyDestination(t, string(out), gateway.URL)
	assertTopologyLogged(t, configRoot, gateway.URL)

	// The sanitizer hosts the catalogue on this path, so startModelCatalogProxy never runs and its bind-address line is never written. Without a record emitted where the transform is BUILT, model-catalog.log would be a 0-byte file exactly when the catalogue is active — which is when a user chasing a wrong /model picker goes looking for it.
	catalogLogged, err := os.ReadFile(filepath.Join(configRoot, "everyapi", "model-catalog.log"))
	if err != nil {
		t.Fatalf("no catalogue log despite the catalogue being active: %v", err)
	}
	if !strings.Contains(string(catalogLogged), "catalogue: claude publishing") {
		t.Fatalf("catalogue log does not record what was published:\n%s", catalogLogged)
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
		if !strings.Contains(b, `"model":"`+realAliasedModel+`"`) {
			t.Fatalf("the gateway did not receive the real model id — the catalogue transform did not run on the sanitizer's socket: %s", b)
		}
	}
}

// assertLaunchLineReportsOnlyDestination checks that the launch line reports the destination and nothing else. The loopback hops are ephemeral ports that change every launch; printing them weighted an implementation detail equally with the answer to "where is my traffic going" and made a working launch read as a complicated one.
//
// Located by the gateway URL rather than by its wording, so the assertion does not depend on which language the CLI resolved.
func assertLaunchLineReportsOnlyDestination(t *testing.T, output, gatewayURL string) {
	t.Helper()
	var line string
	for _, candidate := range strings.Split(output, "\n") {
		if strings.Contains(candidate, gatewayURL) {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatalf("no line naming the gateway in output:\n%s", output)
	}
	// The gateway is itself loopback under httptest, so remove it before looking for hop addresses — otherwise the test fixture trips the check that exists for the hops.
	if rest := strings.ReplaceAll(line, gatewayURL, ""); strings.Contains(rest, "127.0.0.1:") {
		t.Fatalf("launch line still exposes a loopback hop: %q", line)
	}
	// Compared against the rendered i18n string rather than a literal marker, so the assertion holds in whichever locale the CLI resolved. It is the same key the non-transparent launch prints: which transport carried the traffic is a detail of how this process reaches the gateway, not something the launch line reports.
	//
	// Exact match, not a substring: a mode marker appended to the line would still contain the base string, so only equality catches one coming back.
	want := fmt.Sprintf(i18n.T("use.launching"), "claude", gatewayURL)
	if strings.TrimSpace(line) != want {
		t.Fatalf("launch line = %q, want %q", line, want)
	}
}

// assertTopologyLogged is the other half: the hops moved to connector.log rather than being dropped. Without this the stdout assertion above would pass just as well if the topology had stopped being recorded anywhere, which is the difference between relocating diagnostics and losing them.
func assertTopologyLogged(t *testing.T, configRoot, gatewayURL string) {
	t.Helper()
	written, err := os.ReadFile(filepath.Join(configRoot, "everyapi", "connector.log"))
	if err != nil {
		t.Fatalf("no connector log to hold the topology: %v", err)
	}
	var logged string
	for _, candidate := range strings.Split(string(written), "\n") {
		if strings.Contains(candidate, "launch:") {
			logged = candidate
			break
		}
	}
	if logged == "" {
		t.Fatalf("connector.log records no launch topology:\n%s", written)
	}
	// Split after " via ", not from "launch:". Slicing from "launch:" leaves the log timestamp and tool name glued onto the first hop, which makes hops[0] unable to equal hops[1] whatever the code does — the distinctness check below would be permanently dead.
	const marker = " via "
	via := strings.Index(logged, marker)
	if via < 0 {
		t.Fatalf("launch log line carries no topology: %q", logged)
	}
	hops := strings.Split(strings.TrimSpace(logged[via+len(marker):]), " → ")
	if len(hops) != 3 {
		t.Fatalf("logged topology names %d hops, want connector + transforms + gateway: %q", len(hops), logged)
	}
	for i, hop := range hops[:2] {
		if !strings.HasPrefix(hop, "http://127.0.0.1:") {
			t.Fatalf("logged hop %d = %q, want a loopback address: %q", i, hop, logged)
		}
	}
	if hops[0] == hops[1] {
		t.Fatalf("the connector and the transform host logged as the same address: %q", logged)
	}
	if hops[2] != gatewayURL {
		t.Fatalf("logged last hop = %q, want the gateway %q", hops[2], gatewayURL)
	}
}

// runChainShim stands in for `claude`: it talks to the connector exactly as the real tool would — through HTTPS_PROXY, trusting NODE_EXTRA_CA_CERTS — and sends a body carrying a secret the sanitizer is expected to mask.
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

	// Discover the catalogue the way a real client's /model picker does, and use whatever it advertises. Hardcoding the alias would let the test pass with the catalogue transform disabled.
	alias := discoverAliasedModel(t, client)

	body := `{"model":"` + alias + `","messages":[{"role":"user","content":"my key is ` + awsKeyFixture + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through connector: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// discoverAliasedModel returns the synthetic id the catalogue transform publishes for realAliasedModel. Its absence means the transform never ran: the gateway serves the real id, and only the transform rewrites it.
func discoverAliasedModel(t *testing.T, client *http.Client) string {
	t.Helper()
	resp, err := client.Get("https://api.anthropic.com/v1/models")
	if err != nil {
		t.Fatalf("model discovery through connector: %v", err)
	}
	defer resp.Body.Close()
	var catalog struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		t.Fatalf("decode discovered catalogue: %v", err)
	}
	for _, model := range catalog.Data {
		if model.DisplayName == realAliasedModel {
			if model.ID == realAliasedModel {
				t.Fatalf("%q was published unaliased; the catalogue transform did not run", model.ID)
			}
			return model.ID
		}
	}
	t.Fatalf("no alias for %q in the discovered catalogue: %+v", realAliasedModel, catalog.Data)
	return ""
}
