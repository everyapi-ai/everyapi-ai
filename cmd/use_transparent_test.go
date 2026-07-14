package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/tools"
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

	session, err := startTransparentConnector(upstream.URL, "real-relay-key")
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
	if _, err := startTransparentConnector("https://api.everyapi.ai", ""); err == nil {
		t.Fatal("missing relay key unexpectedly accepted")
	}
}

func TestStartTransparentLaunchDoesNotExposeRelayKeyOrGateway(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tool, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	launch, err := startTransparentLaunch(tool, "https://api.everyapi.ai", "real-relay-key")
	if err != nil {
		t.Fatal(err)
	}
	defer launch.session.stop()
	for key, value := range launch.env {
		if strings.Contains(value, "real-relay-key") || strings.Contains(value, "api.everyapi.ai") {
			t.Errorf("child env %s leaks private routing value %q", key, value)
		}
	}
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "NO_PROXY", "no_proxy"} {
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
