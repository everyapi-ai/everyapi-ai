package models

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestRunWithNoArgsDoesNotPanic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Run(nil) panicked: %v", r)
		}
	}()
	if err := Run(nil); err == nil {
		t.Fatal("Run(nil) unexpectedly succeeded without credentials")
	}
}

func TestRunListShowsOnlySmartForPromotionalOnlyAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	i18n.SetLanguage(i18n.LangEn)
	t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/models" {
			t.Fatalf("request path = %q, want /api/user/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"promotional_only":true,"required_group":"auto","data":[{"id":"smart-everyapi","vendor":"EveryAPI"},{"id":"gpt-5.6-luna","vendor":"OpenAI"}]}`))
	}))
	defer server.Close()
	requireNoError(t, config.Save(&config.Credentials{APIBase: server.URL, AccessToken: "account-token", UserID: 7}))

	originalOut := cliout.Out
	var output bytes.Buffer
	cliout.Out = &output
	t.Cleanup(func() { cliout.Out = originalOut })

	requireNoError(t, runList(nil))
	got := output.String()
	if !strings.Contains(got, "smart-everyapi") || strings.Contains(got, "gpt-5.6-luna") || !strings.Contains(got, "promotional balance only") || !strings.Contains(got, "auto") {
		t.Fatalf("promotional-only model output =\n%s", got)
	}
	if !strings.Contains(got, "OpenAI Chat Completions") || !strings.Contains(got, "concurrency limits") || strings.Contains(got, "without tools") {
		t.Fatalf("output does not match the current promotional billing contract:\n%s", got)
	}
}

func TestRunListShowsNoModelsWhenDirectoryIsEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	i18n.SetLanguage(i18n.LangEn)
	t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
	}))
	defer server.Close()
	requireNoError(t, config.Save(&config.Credentials{APIBase: server.URL, AccessToken: "account-token", UserID: 7}))

	originalOut := cliout.Out
	var output bytes.Buffer
	cliout.Out = &output
	t.Cleanup(func() { cliout.Out = originalOut })

	requireNoError(t, runList(nil))
	if got := output.String(); !strings.Contains(got, "No models") {
		t.Fatalf("empty model output =\n%s", got)
	}
}

func TestRunGroupsShowsOnlyRequiredGroupForPromotionalOnlyAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	i18n.SetLanguage(i18n.LangEn)
	t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/models":
			_, _ = w.Write([]byte(`{"success":true,"promotional_only":true,"required_group":"auto","data":[{"id":"smart-everyapi","vendor":"EveryAPI"}]}`))
		case "/api/user/groups":
			_, _ = w.Write([]byte(`{"success":true,"data":{"auto":{"id":"auto","name":"Automatic","ratio":"Auto","usable":true},"default":{"id":"default","name":"Standard","ratio":1,"usable":true}}}`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	requireNoError(t, config.Save(&config.Credentials{APIBase: server.URL, AccessToken: "account-token", UserID: 7}))

	originalOut := cliout.Out
	var output bytes.Buffer
	cliout.Out = &output
	t.Cleanup(func() { cliout.Out = originalOut })

	requireNoError(t, runGroups(nil))
	got := output.String()
	if !strings.Contains(got, "id=auto") || strings.Contains(got, "id=default") || !strings.Contains(got, "promotional balance only") {
		t.Fatalf("promotional-only group output =\n%s", got)
	}
}

func TestRunPricingShowsOnlySmartAutoForPromotionalOnlyAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	i18n.SetLanguage(i18n.LangEn)
	t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/models":
			_, _ = w.Write([]byte(`{"success":true,"promotional_only":true,"required_group":"auto","data":[{"id":"smart-everyapi","vendor":"EveryAPI"}]}`))
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"smart-everyapi","model_ratio":1},{"model_name":"gpt-5.6-luna","model_ratio":2}],"group_ratio":{"auto":1,"default":1},"usable_group":{"auto":"Automatic","default":"Standard"}}`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	requireNoError(t, config.Save(&config.Credentials{APIBase: server.URL, AccessToken: "account-token", UserID: 7}))

	originalOut := cliout.Out
	var output bytes.Buffer
	cliout.Out = &output
	t.Cleanup(func() { cliout.Out = originalOut })

	requireNoError(t, runPricing(nil))
	got := output.String()
	if !strings.Contains(got, "smart-everyapi") || strings.Contains(got, "gpt-5.6-luna") || !strings.Contains(got, "id=auto") || strings.Contains(got, "id=default") || !strings.Contains(got, "promotional balance only") {
		t.Fatalf("promotional-only pricing output =\n%s", got)
	}
}

func TestOAuthRelayOnlyCommandsUseRelayPromotionalMetadata(t *testing.T) {
	for _, command := range []struct {
		name string
		run  func() error
	}{
		{name: "list", run: func() error { return runList(nil) }},
		{name: "groups", run: func() error { return runGroups(nil) }},
		{name: "pricing", run: func() error { return runPricing(nil) }},
	} {
		t.Run(command.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			i18n.SetLanguage(i18n.LangEn)
			t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/user/models":
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
				case "/v1/models":
					_, _ = w.Write([]byte(`{"promotional_only":true,"required_group":"auto","data":[{"id":"smart-everyapi","owned_by":"EveryAPI","supported_endpoint_types":["openai"]},{"id":"gpt-5.6-luna","owned_by":"OpenAI","supported_endpoint_types":["openai-response"],"chat_completions_bridge":true}]}`))
				case "/api/user/groups":
					_, _ = w.Write([]byte(`{"success":true,"data":{"auto":{"id":"auto","name":"Automatic","ratio":"Auto","usable":true},"default":{"id":"default","name":"Standard","ratio":1,"usable":true}}}`))
				case "/api/pricing":
					_, _ = w.Write([]byte(`{"data":[{"model_name":"smart-everyapi","model_ratio":1},{"model_name":"gpt-5.6-luna","model_ratio":2}],"group_ratio":{"auto":1,"default":1},"usable_group":{"auto":"Automatic","default":"Standard"}}`))
				default:
					t.Fatalf("unexpected request path %q", r.URL.Path)
				}
			}))
			defer server.Close()
			relayKey := "sk-everyapi-oauth-relay"
			requireNoError(t, config.Save(&config.Credentials{APIBase: server.URL, AccessToken: relayKey, RelayKey: relayKey, OAuthClientID: "everyapi-cli"}))

			originalOut := cliout.Out
			var output bytes.Buffer
			cliout.Out = &output
			t.Cleanup(func() { cliout.Out = originalOut })

			requireNoError(t, command.run())
			got := output.String()
			if !strings.Contains(got, "promotional balance only") || !strings.Contains(got, "smart-everyapi") && command.name != "groups" || strings.Contains(got, "gpt-5.6-luna") || strings.Contains(got, "id=default") {
				t.Fatalf("OAuth relay-only %s output =\n%s", command.name, got)
			}
		})
	}
}

func TestManagementCredentialModelDirectoryUnauthorizedDoesNotFailOpen(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/models" {
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
	}))
	defer server.Close()
	requireNoError(t, config.Save(&config.Credentials{APIBase: server.URL, AccessToken: "management-token", UserID: 7}))

	if err := runGroups(nil); err == nil || !strings.Contains(err.Error(), i18n.T("auth.session_expired")) {
		t.Fatalf("runGroups error = %v, want session expired", err)
	}
}

func TestRunGroupsPreservesRelayOnlyCredentialFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/models":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
		case "/api/user/groups":
			_, _ = w.Write([]byte(`{"success":true,"data":{"default":{"id":"default","name":"Standard","ratio":1,"usable":true}}}`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	requireNoError(t, config.Save(&config.Credentials{APIBase: server.URL, AccessToken: "relay-only-token"}))

	originalOut := cliout.Out
	var output bytes.Buffer
	cliout.Out = &output
	t.Cleanup(func() { cliout.Out = originalOut })

	requireNoError(t, runGroups(nil))
	if got := output.String(); !strings.Contains(got, "id=default") {
		t.Fatalf("relay-only group output =\n%s", got)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
