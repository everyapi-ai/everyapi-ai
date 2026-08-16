package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestCredentialLockSerializesIndependentCallers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	unlockFirst, err := acquireCredentialLock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlockFirst()

	var acquired atomic.Bool
	done := make(chan struct{})
	go func() {
		unlockSecond, lockErr := acquireCredentialLock()
		if lockErr == nil {
			acquired.Store(true)
			unlockSecond()
		}
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if acquired.Load() {
		t.Fatal("second caller acquired the credential lock before release")
	}
	unlockFirst()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second caller did not acquire the released credential lock")
	}
	if !acquired.Load() {
		t.Fatal("second caller failed to acquire credential lock")
	}
}

func captureCredentialOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	return &out
}

func TestCredentialEmitsVersionedRegionAwareJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	expires := time.Now().Add(48 * time.Hour).Unix()
	if err := config.Save(&config.Credentials{
		APIBase:           config.DefaultAPIBase,
		RelayKey:          "sk-everyapi-test-secret",
		RefreshToken:      "refresh-token",
		OAuthClientID:     "client-id",
		RelayKeyExpiresAt: expires,
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveSettings(&config.Settings{GatewayRegion: "cn"}); err != nil {
		t.Fatal(err)
	}
	out := captureCredentialOutput(t)

	if err := Credential([]string{"--format=json"}); err != nil {
		t.Fatalf("Credential: %v", err)
	}

	var got struct {
		Version   int    `json:"version"`
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out.String(), err)
	}
	if got.Version != 1 || got.BaseURL != config.ChinaAPIBase+"/v1" {
		t.Fatalf("unexpected credential metadata: %+v", got)
	}
	if got.APIKey != "sk-everyapi-test-secret" {
		t.Fatalf("api_key = %q", got.APIKey)
	}
	wantExpiry := time.Unix(expires, 0).UTC().Format(time.RFC3339)
	if got.ExpiresAt != wantExpiry {
		t.Fatalf("expires_at = %q, want %q", got.ExpiresAt, wantExpiry)
	}
}

func TestCredentialOmitsExpirationForLongLivedKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{
		APIBase:  "https://self-hosted.example",
		RelayKey: "sk-everyapi-long-lived",
	}); err != nil {
		t.Fatal(err)
	}
	out := captureCredentialOutput(t)

	if err := Credential([]string{"--format", "json"}); err != nil {
		t.Fatalf("Credential: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, exists := got["expires_at"]; exists {
		t.Fatalf("non-expiring credential emitted expires_at: %s", out.String())
	}
	if got["base_url"] != "https://self-hosted.example/v1" {
		t.Fatalf("base_url = %v", got["base_url"])
	}
}

func TestCredentialCanIncludeTheRelayScopedModelCatalog(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-everyapi-models" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"image-only","owned_by":"vendor","supported_endpoint_types":["image-generation"]},{"id":"chat-model","owned_by":"vendor","supported_endpoint_types":["openai"],"chat_completions_bridge":true},{"id":"no-endpoint","owned_by":"vendor","supported_endpoint_types":[]}]}`))
	}))
	defer srv.Close()
	if err := config.Save(&config.Credentials{
		APIBase:  srv.URL,
		RelayKey: "sk-everyapi-models",
	}); err != nil {
		t.Fatal(err)
	}
	out := captureCredentialOutput(t)

	if err := Credential([]string{"--format=json", "--include-models"}); err != nil {
		t.Fatalf("Credential: %v", err)
	}
	var got struct {
		Models []struct {
			ID                     string   `json:"id"`
			SupportedEndpointTypes []string `json:"supported_endpoint_types"`
			ChatCompletionsBridge  bool     `json:"chat_completions_bridge"`
		} `json:"models"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 3 || got.Models[0].ID != "chat-model" || got.Models[0].SupportedEndpointTypes[0] != "openai" {
		t.Fatalf("models = %#v", got.Models)
	}
	if !got.Models[0].ChatCompletionsBridge {
		t.Fatal("credential model did not preserve chat_completions_bridge")
	}
	var raw struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if endpoints, ok := raw.Models[2]["supported_endpoint_types"]; !ok {
		t.Fatal("credential model omitted an explicit empty supported_endpoint_types field")
	} else if values, ok := endpoints.([]any); !ok || len(values) != 0 {
		t.Fatalf("supported_endpoint_types = %#v, want []", endpoints)
	}
}

func TestCredentialInvalidateSelectsAReplacementKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{"items": []map[string]any{{
					"id": 9, "name": "replacement", "status": 1, "group": "",
				}}},
			})
		case "/api/token/9/key":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"key": "sk-everyapi-replacement"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	if err := config.Save(&config.Credentials{
		APIBase:         srv.URL,
		AccessToken:     "management-token",
		UserID:          7,
		RelayKey:        "sk-everyapi-rejected",
		RelayKeyTokenID: 3,
	}); err != nil {
		t.Fatal(err)
	}
	out := captureCredentialOutput(t)

	if err := Credential([]string{"--format=json", "--invalidate"}); err != nil {
		t.Fatalf("Credential invalidate: %v", err)
	}
	if strings.Contains(out.String(), "sk-everyapi-rejected") ||
		!strings.Contains(out.String(), "sk-everyapi-replacement") {
		t.Fatalf("unexpected output: %s", out.String())
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RelayKey != "sk-everyapi-replacement" || reloaded.RelayKeyTokenID != 9 {
		t.Fatalf("replacement not persisted: %+v", reloaded)
	}
}

func TestCredentialInvalidateForcesOAuthRefresh(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth2/token" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "sk-everyapi-oauth-fresh",
			"refresh_token": "rt-fresh",
			"expires_in":    172800,
		})
	}))
	defer srv.Close()
	if err := config.Save(&config.Credentials{
		APIBase:           srv.URL,
		AccessToken:       "sk-everyapi-oauth-rejected",
		RelayKey:          "sk-everyapi-oauth-rejected",
		RefreshToken:      "rt-old",
		OAuthClientID:     "cli-1",
		RelayKeyExpiresAt: time.Now().Add(48 * time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	out := captureCredentialOutput(t)

	if err := Credential([]string{"--format=json", "--invalidate"}); err != nil {
		t.Fatalf("Credential invalidate: %v", err)
	}
	if strings.Contains(out.String(), "oauth-rejected") ||
		!strings.Contains(out.String(), "oauth-fresh") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestCredentialErrorsHaveStableMachineCodes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	err := Credential([]string{"--format=json"})
	if err == nil || !strings.Contains(err.Error(), "EVERYAPI_CREDENTIAL_ERROR:not_logged_in") {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthRoutesCredentialSubcommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{
		APIBase:  config.DefaultAPIBase,
		RelayKey: "sk-everyapi-routed",
	}); err != nil {
		t.Fatal(err)
	}
	out := captureCredentialOutput(t)
	if err := Auth([]string{"credential", "--format=json"}); err != nil {
		t.Fatalf("Auth credential: %v", err)
	}
	if !strings.Contains(out.String(), `"version":1`) {
		t.Fatalf("credential JSON not routed: %q", out.String())
	}
}
