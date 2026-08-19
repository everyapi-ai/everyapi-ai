package artifacts

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestPublishUploadsHTMLWithTheCurrentSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const html = "<!doctype html><title>验收报告</title>"
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		if r.Method != http.MethodPost || r.URL.Path != "/api/artifacts" {
			t.Errorf("request = %s %s, want POST /api/artifacts", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("EveryAPI-User-Id"); got != "42" {
			t.Errorf("EveryAPI-User-Id = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := r.Header.Get("X-EveryAPI-Auth-Origin"); got != config.DefaultAPIBase {
			t.Errorf("X-EveryAPI-Auth-Origin = %q", got)
		}
		encodedName := r.Header.Get("X-Artifact-Filename")
		decodedName, err := base64.RawURLEncoding.DecodeString(encodedName)
		if err != nil {
			t.Fatalf("decode filename: %v", err)
		}
		if string(decodedName) != "报告.html" {
			t.Errorf("filename = %q", decodedName)
		}
		body := new(bytes.Buffer)
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		if body.String() != html {
			t.Errorf("body = %q", body.String())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"url":"https://artifacts.everyapi.ai/TK4tBA9HQErZ","expires_at":"2026-09-18T12:00:00Z"}`))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "报告.html")
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := publish(context.Background(), server.Client(), server.URL, &config.Credentials{APIBase: config.DefaultAPIBase, AccessToken: "access-token", UserID: 42}, path)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !received {
		t.Fatal("artifact service was not called")
	}
	if result.URL != "https://artifacts.everyapi.ai/TK4tBA9HQErZ" || result.ExpiresAt != "2026-09-18T12:00:00Z" {
		t.Errorf("result = %+v", result)
	}
}

func TestPublishRejectsInvalidFilesBeforeUpload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	creds := &config.Credentials{AccessToken: "access-token", UserID: 42}

	t.Run("self-hosted credentials", func(t *testing.T) {
		selfHosted := &config.Credentials{APIBase: "https://selfhost.example", AccessToken: "private-token", UserID: 42}
		path := filepath.Join(t.TempDir(), "report.html")
		if err := os.WriteFile(path, []byte("<html></html>"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := publish(context.Background(), server.Client(), server.URL, selfHosted, path); err == nil {
			t.Fatal("want an error before a self-hosted token crosses into the hosted artifact service")
		}
	})

	t.Run("non-html extension", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "report.txt")
		if err := os.WriteFile(path, []byte("<html></html>"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := publish(context.Background(), server.Client(), server.URL, creds, path); err == nil {
			t.Fatal("want an error for a non-HTML file")
		}
	})

	t.Run("oversized html", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "report.html")
		if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(maxArtifactBytes+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := publish(context.Background(), server.Client(), server.URL, creds, path); err == nil {
			t.Fatal("want an error for an oversized artifact")
		}
	})

	if requests != 0 {
		t.Fatalf("invalid files made %d upload requests", requests)
	}
}

func TestPublishRejectsInvalidServiceResponses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	if err := os.WriteFile(path, []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	creds := &config.Credentials{APIBase: config.DefaultAPIBase, AccessToken: "access-token", UserID: 42}
	tests := []struct {
		name string
		body string
	}{
		{name: "unexpected host", body: `{"url":"https://evil.example/TK4tBA9HQErZ","expires_at":"2026-09-18T12:00:00Z"}`},
		{name: "unexpected id", body: `{"url":"https://artifacts.everyapi.ai/not-valid","expires_at":"2026-09-18T12:00:00Z"}`},
		{name: "query injection", body: `{"url":"https://artifacts.everyapi.ai/TK4tBA9HQErZ?next=evil","expires_at":"2026-09-18T12:00:00Z"}`},
		{name: "invalid expiry", body: `{"url":"https://artifacts.everyapi.ai/TK4tBA9HQErZ","expires_at":"not-a-time"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			if _, err := publish(context.Background(), server.Client(), server.URL, creds, path); err == nil {
				t.Fatal("want an error for an invalid service response")
			}
		})
	}
}

func TestArtifactHTTPClientHasATimeout(t *testing.T) {
	if httpClient.Timeout <= 0 || httpClient.Timeout > 2*time.Minute {
		t.Fatalf("httpClient.Timeout = %v, want a positive timeout no longer than two minutes", httpClient.Timeout)
	}
}

func TestRunSharePrintsOnlyThePublicURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"url":"https://artifacts.everyapi.ai/TK4tBA9HQErZ","expires_at":"2026-09-18T12:00:00Z"}`))
	}))
	defer server.Close()
	previousBase, previousClient := serviceBaseURL, httpClient
	serviceBaseURL, httpClient = server.URL, server.Client()
	t.Cleanup(func() { serviceBaseURL, httpClient = previousBase, previousClient })

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{AccessToken: "access-token", UserID: 42}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := os.WriteFile(path, []byte("<!doctype html><title>Report</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	previousOut := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previousOut })

	if err := Run([]string{"share", path}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "https://artifacts.everyapi.ai/TK4tBA9HQErZ" {
		t.Errorf("stdout = %q", got)
	}
}

func TestRunRequiresSharePathAndCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Run([]string{"share"}); err == nil {
		t.Fatal("want an error when the path is missing")
	}
	if err := Run([]string{"share", "/tmp/report.html"}); err == nil {
		t.Fatal("want an error when signed out")
	}
}
