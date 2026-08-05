//go:build !windows

package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

const transparentUseHelperEnv = "EVERYAPI_TEST_TRANSPARENT_USE_HELPER"

type transparentModelCapture struct {
	authorization string
	providerKey   string
	body          string
}

func TestUseTransparentChildEnvironmentAndCleanup(t *testing.T) {
	if os.Getenv(transparentUseHelperEnv) == "1" {
		if err := Use([]string{"claude", "--transparent"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(98)
		}
		os.Exit(99)
		return
	}

	probeAuth := make(chan string, 1)
	modelRequest := make(chan transparentModelCapture, 1)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
			body, _ := io.ReadAll(r.Body)
			modelRequest <- transparentModelCapture{
				authorization: r.Header.Get("Authorization"),
				providerKey:   r.Header.Get("X-Api-Key"),
				body:          string(body),
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"relayed":true}`))
			return
		}
		probeAuth <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-test","owned_by":"anthropic","supported_endpoint_types":["anthropic"]}]}`))
	}))
	defer gateway.Close()

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	const relayKey = "integration-real-relay-key"
	if err := config.Save(&config.Credentials{APIBase: gateway.URL, RelayKey: relayKey}); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	envPath := filepath.Join(t.TempDir(), "child.env")
	caCopyPath := filepath.Join(t.TempDir(), "child-ca.pem")
	caPathRecord := filepath.Join(t.TempDir(), "ca-path")
	shim := filepath.Join(binDir, "claude")
	script := "#!/bin/sh\n" +
		"env > \"$EVERYAPI_TEST_CHILD_ENV\"\n" +
		"cp \"$NODE_EXTRA_CA_CERTS\" \"$EVERYAPI_TEST_CA_COPY\"\n" +
		"printf '%s' \"$NODE_EXTRA_CA_CERTS\" > \"$EVERYAPI_TEST_CA_PATH\"\n" +
		"EVERYAPI_TEST_TRANSPARENT_HTTP_HELPER=1 \"$EVERYAPI_TEST_BINARY\" -test.run=^TestTransparentChildModelRequest$ -test.v=false\n"
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestUseTransparentChildEnvironmentAndCleanup$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		transparentUseHelperEnv+"=1",
		"XDG_CONFIG_HOME="+xdg,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"EVERYAPI_TEST_CHILD_ENV="+envPath,
		"EVERYAPI_TEST_CA_COPY="+caCopyPath,
		"EVERYAPI_TEST_CA_PATH="+caPathRecord,
		"EVERYAPI_TEST_BINARY="+os.Args[0],
		"ANTHROPIC_BASE_URL=https://ambient.invalid",
		"ANTHROPIC_API_KEY=ambient-real-provider-key",
		"EVERYAPI_RELAY_KEY=ambient-relay-key",
		"NO_PROXY=api.anthropic.com",
		"no_proxy=api.anthropic.com",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("helper exit=%d output=%s", exitErr.ExitCode(), output)
		}
		t.Fatalf("helper: %v output=%s", err, output)
	}
	if got := <-probeAuth; got != "Bearer "+relayKey {
		t.Fatalf("relay probe Authorization = %q", got)
	}
	model := <-modelRequest
	if model.authorization != "Bearer "+relayKey || model.providerKey != "" || model.body != `{"model":"claude-test"}` {
		t.Fatalf("relayed model request = %#v", model)
	}

	envBody, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	envText := string(envBody)
	for _, want := range []string{
		"ANTHROPIC_AUTH_TOKEN=everyapi-local-connector\n",
		"HTTPS_PROXY=http://127.0.0.1:",
		"https_proxy=http://127.0.0.1:",
		"NODE_EXTRA_CA_CERTS=",
	} {
		if !strings.Contains(envText, want) {
			t.Errorf("child env missing %q:\n%s", want, envText)
		}
	}
	for _, forbidden := range []string{
		"ANTHROPIC_BASE_URL=", "ANTHROPIC_API_KEY=", "EVERYAPI_RELAY_KEY=",
		"NO_PROXY=", "no_proxy=", relayKey, "ambient-real-provider-key", "ambient-relay-key",
	} {
		if strings.Contains(envText, forbidden) {
			t.Errorf("child env contains forbidden %q", forbidden)
		}
	}
	caCopy, err := os.ReadFile(caCopyPath)
	if err != nil || !strings.Contains(string(caCopy), "BEGIN CERTIFICATE") || strings.Contains(string(caCopy), "PRIVATE KEY") {
		t.Fatalf("child CA copy invalid or private: err=%v body=%q", err, caCopy)
	}
	caPathBody, err := os.ReadFile(caPathRecord)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(caPathBody)); !os.IsNotExist(err) {
		t.Errorf("temporary CA was not removed after child exit: %v", err)
	}
}

func TestTransparentChildModelRequest(t *testing.T) {
	if os.Getenv("EVERYAPI_TEST_TRANSPARENT_HTTP_HELPER") != "1" {
		t.Skip("helper process only")
	}
	caPEM, err := os.ReadFile(os.Getenv("NODE_EXTRA_CA_CERTS"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("no CA certificate")
	}
	proxy, err := url.Parse(os.Getenv("HTTPS_PROXY"))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxy),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
	req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(`{"model":"claude-test"}`))
	req.Header.Set("Authorization", "Bearer child-provider-secret")
	req.Header.Set("X-Api-Key", "child-provider-api-key")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != `{"relayed":true}` {
		t.Fatalf("response status=%d body=%q err=%v", resp.StatusCode, body, err)
	}
}
