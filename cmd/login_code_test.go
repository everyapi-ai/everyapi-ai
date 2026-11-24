package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

type fakeCodeRedeemer struct {
	results []redeemResult
	codes   []string
}

type redeemResult struct {
	res *api.DeviceAuthPollResult
	err error
}

func (f *fakeCodeRedeemer) RedeemCLIAuthCode(_ context.Context, code string) (*api.DeviceAuthPollResult, error) {
	f.codes = append(f.codes, code)
	r := f.results[0]
	f.results = f.results[1:]
	return r.res, r.err
}

func codeReader(answers ...string) func() (string, error) {
	return func() (string, error) {
		if len(answers) == 0 {
			return "", io.EOF
		}
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
}

func withLoginCodeInteractive(t *testing.T, interactive bool) {
	t.Helper()
	previous := loginCodeInteractive
	loginCodeInteractive = func() bool { return interactive }
	t.Cleanup(func() { loginCodeInteractive = previous })
}

func captureLoginOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := cliout.Out
	cliout.Out = &buf
	t.Cleanup(func() { cliout.Out = original })
	return &buf
}

func selfServer(t *testing.T, accessToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Errorf("self Authorization = %q", got)
			}
			_, _ = io.WriteString(w, `{"success":true,"data":{"id":7,"username":"alice","role":1}}`)
		case "/api/token/":
			_, _ = io.WriteString(w, `{"success":true,"data":{"items":[{"id":11,"name":"cli","status":1,"group":""}]}}`)
		case "/api/token/11/key":
			_, _ = io.WriteString(w, `{"success":true,"data":{"key":"sk-everyapi-relay"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginWithAuthCode_PersistsCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withLoginCodeInteractive(t, false)
	out := captureLoginOutput(t)
	srv := selfServer(t, "management-access")
	client := &fakeCodeRedeemer{results: []redeemResult{{res: &api.DeviceAuthPollResult{
		State: api.PollAuthorized, AccessToken: "management-access", UserID: 7, Username: "alice",
	}}}}

	if err := loginWithAuthCode(context.Background(), srv.URL, "", client, codeReader("  WDJB-MJHT-KQ7R-2NXP \n")); err != nil {
		t.Fatalf("loginWithAuthCode: %v", err)
	}
	if len(client.codes) != 1 || client.codes[0] != "WDJB-MJHT-KQ7R-2NXP" {
		t.Fatalf("redeemed codes = %q", client.codes)
	}
	if !strings.Contains(out.String(), srv.URL+cliAuthCodePath) {
		t.Fatalf("output does not point at the code page: %s", out.String())
	}
	creds, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != "management-access" || creds.UserID != 7 || creds.Username != "alice" || creds.RelayKey != "sk-everyapi-relay" {
		t.Fatalf("credentials not persisted: %+v", creds)
	}
}

func TestLoginWithAuthCode_RetriesRejectedCodeInteractively(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withLoginCodeInteractive(t, true)
	captureLoginOutput(t)
	srv := selfServer(t, "management-access")
	client := &fakeCodeRedeemer{results: []redeemResult{
		{err: &api.ErrCLIAuthCodeInvalid{Message: "Code not found or expired"}},
		{res: &api.DeviceAuthPollResult{State: api.PollAuthorized, AccessToken: "management-access", UserID: 7, Username: "alice"}},
	}}

	if err := loginWithAuthCode(context.Background(), srv.URL, "", client, codeReader("BAD", "GOOD")); err != nil {
		t.Fatalf("loginWithAuthCode: %v", err)
	}
	if strings.Join(client.codes, ",") != "BAD,GOOD" {
		t.Fatalf("redeemed codes = %q", client.codes)
	}
}

func TestLoginWithAuthCode_Failures(t *testing.T) {
	invalid := &api.ErrCLIAuthCodeInvalid{Message: "Code not found or expired"}
	cases := []struct {
		name        string
		interactive bool
		results     []redeemResult
		answers     []string
		wantErr     string
	}{
		{"non-interactive gets one attempt", false, []redeemResult{{err: invalid}}, []string{"BAD"}, fmt.Sprintf(i18n.T("login.code_rejected"), "Code not found or expired")},
		{"interactive gives up after the limit", true, []redeemResult{{err: invalid}, {err: invalid}, {err: invalid}}, []string{"A", "B", "C"}, i18n.T("login.code_attempts_exhausted")},
		{"gateway without the endpoint", false, []redeemResult{{err: &api.APIError{StatusCode: http.StatusNotFound}}}, []string{"CODE"}, i18n.T("login.code_unsupported")},
		{"empty stdin", false, nil, nil, i18n.T("login.code_missing")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			withLoginCodeInteractive(t, tc.interactive)
			captureLoginOutput(t)
			client := &fakeCodeRedeemer{results: tc.results}
			err := loginWithAuthCode(context.Background(), "https://api.everyapi.ai", "", client, codeReader(tc.answers...))
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if creds, _ := config.Load(); creds != nil && creds.AccessToken != "" {
				t.Fatalf("failed login persisted credentials: %+v", creds)
			}
		})
	}
}

func TestLoginWithAuthCode_CancelIsClean(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withLoginCodeInteractive(t, true)
	out := captureLoginOutput(t)
	cancelled := func() (string, error) { return "", cliprompt.ErrPickCancelled }
	if err := loginWithAuthCode(context.Background(), "https://api.everyapi.ai", "", &fakeCodeRedeemer{}, cancelled); err != nil {
		t.Fatalf("cancel returned %v", err)
	}
	if !strings.Contains(out.String(), i18n.T("login.cancelled")) {
		t.Fatalf("output = %s", out.String())
	}
	if !strings.Contains(out.String(), "https://app.everyapi.ai"+cliAuthCodePath) {
		t.Fatalf("official gateway should point at the app host: %s", out.String())
	}
}

func TestLoginWithAuthCode_CancelWhileReadBlocks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withLoginCodeInteractive(t, false)
	out := captureLoginOutput(t)
	ctx, cancel := context.WithCancel(context.Background())
	blocked := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	reader := func() (string, error) {
		close(blocked)
		<-release
		return "", io.EOF
	}
	go func() {
		<-blocked
		cancel()
	}()

	done := make(chan error, 1)
	go func() { done <- loginWithAuthCode(ctx, "https://api.everyapi.ai", "", &fakeCodeRedeemer{}, reader) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancel returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login did not return after its context was cancelled while the read was blocked")
	}
	if !strings.Contains(out.String(), i18n.T("login.cancelled")) {
		t.Fatalf("output = %s", out.String())
	}
}
