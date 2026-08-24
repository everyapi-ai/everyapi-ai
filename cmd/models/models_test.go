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

func TestRunListExplainsPromotionalOnlyRestriction(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	i18n.SetLanguage(i18n.LangEn)
	t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/models" {
			t.Fatalf("request path = %q, want /api/user/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"promotional_only":true,"required_group":"auto","data":[{"id":"smart-everyapi","vendor":"EveryAPI"}]}`))
	}))
	defer server.Close()
	requireNoError(t, config.Save(&config.Credentials{APIBase: server.URL, AccessToken: "account-token", UserID: 7}))

	originalOut := cliout.Out
	var output bytes.Buffer
	cliout.Out = &output
	t.Cleanup(func() { cliout.Out = originalOut })

	requireNoError(t, runList(nil))
	got := output.String()
	if !strings.Contains(got, "promotional balance only") || !strings.Contains(got, "auto") || !strings.Contains(got, "smart-everyapi") {
		t.Fatalf("output does not explain the promotional-only restriction:\n%s", got)
	}
}

func TestRunListExplainsPromotionalRestrictionWhenSmartModelIsUnavailable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	i18n.SetLanguage(i18n.LangEn)
	t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"promotional_only":true,"required_group":"auto","data":[]}`))
	}))
	defer server.Close()
	requireNoError(t, config.Save(&config.Credentials{APIBase: server.URL, AccessToken: "account-token", UserID: 7}))

	originalOut := cliout.Out
	var output bytes.Buffer
	cliout.Out = &output
	t.Cleanup(func() { cliout.Out = originalOut })

	requireNoError(t, runList(nil))
	if got := output.String(); !strings.Contains(got, "promotional balance only") || !strings.Contains(got, "No models") {
		t.Fatalf("empty promotional-only model output =\n%s", got)
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
	if !strings.Contains(got, "id=auto") || strings.Contains(got, "id=default") {
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
			_, _ = w.Write([]byte(`{"data":[{"model_name":"smart-everyapi","model_ratio":1},{"model_name":"gpt-paid","model_ratio":2}],"group_ratio":{"auto":1,"default":1},"usable_group":{"auto":"Automatic","default":"Standard"}}`))
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
	if !strings.Contains(got, "smart-everyapi") || !strings.Contains(got, "id=auto") || strings.Contains(got, "gpt-paid") || strings.Contains(got, "id=default") {
		t.Fatalf("promotional-only pricing output =\n%s", got)
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
