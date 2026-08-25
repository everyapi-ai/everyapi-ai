package artifacts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestPublishUploadsHTMLWithTheCurrentSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	projectDir := filepath.Join(t.TempDir(), "everyapi-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)
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
		encodedProject := r.Header.Get("X-Artifact-Project")
		decodedProject, err := base64.RawURLEncoding.DecodeString(encodedProject)
		if err != nil {
			t.Fatalf("decode project: %v", err)
		}
		if string(decodedProject) != "everyapi-project" {
			t.Errorf("project = %q", decodedProject)
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

func TestArtifactProjectFromRemote(t *testing.T) {
	tests := map[string]string{
		"git@github.com:everyapi-ai/everyapi.git":     "everyapi",
		"https://github.com/everyapi-ai/everyapi.git": "everyapi",
		"https://example.com/团队/验收项目.git":             "验收项目",
	}
	for remote, want := range tests {
		if got := artifactProjectFromRemote(remote); got != want {
			t.Errorf("artifactProjectFromRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestArtifactHTTPClientRefusesRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedRequests.Add(1)
		http.Error(w, "must not be reached", http.StatusInternalServerError)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	path := writeArtifactFile(t, "<html></html>")
	creds := &config.Credentials{APIBase: config.DefaultAPIBase, AccessToken: "access-token", UserID: 42}
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "share", run: func() error {
			_, err := publish(context.Background(), httpClient, source.URL, creds, path)
			return err
		}},
		{name: "update", run: func() error {
			_, err := updateArtifact(context.Background(), httpClient, source.URL, creds, "https://artifacts.everyapi.ai/TK4tBA9HQErZ", path)
			return err
		}},
		{name: "delete", run: func() error {
			_, err := deleteArtifact(context.Background(), httpClient, source.URL, creds, "https://artifacts.everyapi.ai/TK4tBA9HQErZ")
			return err
		}},
		{name: "list", run: func() error {
			_, err := listArtifacts(context.Background(), httpClient, source.URL, creds)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redirectedRequests.Store(0)
			if err := test.run(); err == nil {
				t.Fatal("redirect response must not be accepted as an artifact result")
			}
			if got := redirectedRequests.Load(); got != 0 {
				t.Fatalf("artifact client followed the redirect and sent %d request(s) to the second server", got)
			}
		})
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

func TestRunUpdateReplacesTheOwnedArtifact(t *testing.T) {
	const html = "<!doctype html><title>Updated</title>"
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		if r.Method != http.MethodPut || r.URL.Path != "/api/artifacts/TK4tBA9HQErZ" {
			t.Errorf("request = %s %s, want PUT /api/artifacts/TK4tBA9HQErZ", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access-token" || r.Header.Get("EveryAPI-User-Id") != "42" {
			t.Errorf("management credentials were not forwarded")
		}
		body := new(bytes.Buffer)
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		if body.String() != html {
			t.Errorf("body = %q", body.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://artifacts.everyapi.ai/TK4tBA9HQErZ","expires_at":"2026-09-18T12:00:00Z"}`))
	}))
	defer server.Close()
	configureRunTest(t, server)
	path := writeArtifactFile(t, html)
	out := captureArtifactOutput(t)

	if err := Run([]string{"update", "https://artifacts.everyapi.ai/TK4tBA9HQErZ", path}); err != nil {
		t.Fatalf("Run update: %v", err)
	}
	if !received {
		t.Fatal("artifact service was not called")
	}
	if got := strings.TrimSpace(out.String()); got != "https://artifacts.everyapi.ai/TK4tBA9HQErZ" {
		t.Errorf("stdout = %q", got)
	}
}

func TestRunUpdateRejectsAResponseForADifferentArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://artifacts.everyapi.ai/NJR7itv46u5X","expires_at":"2026-09-18T12:00:00Z"}`))
	}))
	defer server.Close()
	configureRunTest(t, server)
	path := writeArtifactFile(t, "<html>updated</html>")

	err := Run([]string{"update", "https://artifacts.everyapi.ai/TK4tBA9HQErZ", path})
	if err == nil || !strings.Contains(err.Error(), "TK4tBA9HQErZ") {
		t.Fatalf("Run update error = %v, want mismatched artifact URL error", err)
	}
}

func TestRunDeleteRevokesTheOwnedArtifact(t *testing.T) {
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		if r.Method != http.MethodDelete || r.URL.Path != "/api/artifacts/TK4tBA9HQErZ" {
			t.Errorf("request = %s %s, want DELETE /api/artifacts/TK4tBA9HQErZ", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	configureRunTest(t, server)
	out := captureArtifactOutput(t)

	if err := Run([]string{"delete", "https://artifacts.everyapi.ai/TK4tBA9HQErZ"}); err != nil {
		t.Fatalf("Run delete: %v", err)
	}
	if !received {
		t.Fatal("artifact service was not called")
	}
	if got := strings.TrimSpace(out.String()); got != "deleted https://artifacts.everyapi.ai/TK4tBA9HQErZ" {
		t.Errorf("stdout = %q", got)
	}
}

func TestRunListFollowsPaginationAndPrintsNewestFirst(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/api/artifacts" {
			t.Errorf("request = %s %s, want GET /api/artifacts", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access-token" || r.Header.Get("EveryAPI-User-Id") != "42" {
			t.Errorf("listing credentials were not forwarded")
		}
		if r.Header.Get("X-EveryAPI-Auth-Origin") != config.DefaultAPIBase {
			t.Errorf("auth origin = %q", r.Header.Get("X-EveryAPI-Auth-Origin"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"artifacts":[],"next_cursor":"opaque cursor/+"}`))
		case "opaque cursor/+":
			_, _ = w.Write([]byte(`{"artifacts":[{"url":"https://artifacts.everyapi.ai/TK4tBA9HQErZ","filename":"older.html","created_at":"2026-08-18T12:00:00Z","expires_at":"2026-09-17T12:00:00Z"},{"url":"https://artifacts.everyapi.ai/NJR7itv46u5X","filename":"newer.html","created_at":"2026-08-19T12:00:00Z","updated_at":"2026-08-20T12:00:00Z","expires_at":"2026-09-18T12:00:00Z"}]}`))
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
		}
	}))
	defer server.Close()
	configureRunTest(t, server)
	out := captureArtifactOutput(t)

	if err := Run([]string{"list"}); err != nil {
		t.Fatalf("Run list: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	want := "https://artifacts.everyapi.ai/NJR7itv46u5X\nhttps://artifacts.everyapi.ai/TK4tBA9HQErZ\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunListJSONIncludesManagementMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artifacts":[{"url":"https://artifacts.everyapi.ai/TK4tBA9HQErZ","filename":"report.html","title":"Release report","project":"everyapi","created_at":"2026-08-18T12:00:00Z","expires_at":"2026-09-17T12:00:00Z"}]}`))
	}))
	defer server.Close()
	configureRunTest(t, server)
	out := captureArtifactOutput(t)

	if err := Run([]string{"list", "--json"}); err != nil {
		t.Fatalf("Run list --json: %v", err)
	}
	var got artifactListResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", out.String(), err)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Filename != "report.html" || got.Artifacts[0].Title != "Release report" || got.Artifacts[0].Project != "everyapi" {
		t.Fatalf("JSON = %#v", got)
	}
}

func TestListRejectsInvalidServicePages(t *testing.T) {
	creds := &config.Credentials{APIBase: config.DefaultAPIBase, AccessToken: "access-token", UserID: 42}
	tests := []struct {
		name string
		body string
	}{
		{name: "untrusted URL", body: `{"artifacts":[{"url":"https://evil.example/TK4tBA9HQErZ","filename":"report.html","created_at":"2026-08-18T12:00:00Z","expires_at":"2026-09-17T12:00:00Z"}]}`},
		{name: "invalid expiry", body: `{"artifacts":[{"url":"https://artifacts.everyapi.ai/TK4tBA9HQErZ","filename":"report.html","created_at":"2026-08-18T12:00:00Z","expires_at":"never"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			if _, err := listArtifacts(context.Background(), server.Client(), server.URL, creds); err == nil {
				t.Fatal("want an error for an invalid list response")
			}
		})
	}
}

func TestRunJSONEmitsMachineReadableResults(t *testing.T) {
	tests := []struct {
		name       string
		args       func(string) []string
		wantMethod string
		wantStatus int
		wantJSON   map[string]any
	}{
		{
			name:       "share",
			args:       func(path string) []string { return []string{"share", "--json", path} },
			wantMethod: http.MethodPost,
			wantStatus: http.StatusCreated,
			wantJSON:   map[string]any{"url": "https://artifacts.everyapi.ai/TK4tBA9HQErZ", "expires_at": "2026-09-18T12:00:00Z"},
		},
		{
			name: "update",
			args: func(path string) []string {
				return []string{"update", "--json", "https://artifacts.everyapi.ai/TK4tBA9HQErZ", path}
			},
			wantMethod: http.MethodPut,
			wantStatus: http.StatusOK,
			wantJSON:   map[string]any{"url": "https://artifacts.everyapi.ai/TK4tBA9HQErZ", "expires_at": "2026-09-18T12:00:00Z"},
		},
		{
			name: "delete",
			args: func(string) []string {
				return []string{"delete", "--json", "https://artifacts.everyapi.ai/TK4tBA9HQErZ"}
			},
			wantMethod: http.MethodDelete,
			wantStatus: http.StatusNoContent,
			wantJSON:   map[string]any{"url": "https://artifacts.everyapi.ai/TK4tBA9HQErZ", "deleted": true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.wantMethod {
					t.Errorf("method = %s, want %s", r.Method, test.wantMethod)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.wantStatus)
				if test.wantStatus != http.StatusNoContent {
					_, _ = w.Write([]byte(`{"url":"https://artifacts.everyapi.ai/TK4tBA9HQErZ","expires_at":"2026-09-18T12:00:00Z"}`))
				}
			}))
			defer server.Close()
			configureRunTest(t, server)
			out := captureArtifactOutput(t)

			if err := Run(test.args(writeArtifactFile(t, "<html></html>"))); err != nil {
				t.Fatalf("Run: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("stdout is not JSON: %q: %v", out.String(), err)
			}
			if len(got) != len(test.wantJSON) {
				t.Fatalf("JSON = %#v, want %#v", got, test.wantJSON)
			}
			for key, want := range test.wantJSON {
				if got[key] != want {
					t.Errorf("JSON[%q] = %#v, want %#v", key, got[key], want)
				}
			}
		})
	}
}

func TestRunRejectsUntrustedArtifactURLsBeforeSendingCredentials(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	configureRunTest(t, server)
	path := writeArtifactFile(t, "<html></html>")

	for _, args := range [][]string{
		{"update", "https://evil.example/TK4tBA9HQErZ", path},
		{"delete", "https://artifacts.everyapi.ai/TK4tBA9HQErZ?redirect=evil"},
		{"delete", "not-an-artifact"},
	} {
		if err := Run(args); err == nil || !strings.Contains(err.Error(), "invalid artifact URL") {
			t.Fatalf("Run(%q) error = %v, want an invalid artifact URL error", args, err)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid URLs made %d requests", requests)
	}
}

func configureRunTest(t *testing.T, server *httptest.Server) {
	t.Helper()
	previousBase, previousClient := serviceBaseURL, httpClient
	serviceBaseURL, httpClient = server.URL, server.Client()
	t.Cleanup(func() { serviceBaseURL, httpClient = previousBase, previousClient })
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{APIBase: config.DefaultAPIBase, AccessToken: "access-token", UserID: 42}); err != nil {
		t.Fatal(err)
	}
}

func writeArtifactFile(t *testing.T, html string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.html")
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureArtifactOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	out := &bytes.Buffer{}
	previous := cliout.Out
	cliout.Out = out
	t.Cleanup(func() { cliout.Out = previous })
	return out
}
