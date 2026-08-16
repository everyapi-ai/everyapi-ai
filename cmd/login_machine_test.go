package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

type fakeMachineLoginClient struct {
	start        *api.DeviceAuthStartResp
	startErr     error
	poll         *api.DeviceAuthPollResult
	pollErr      error
	oauthStart   *api.DeviceAuthStartResp
	oauthErr     error
	oauthToken   *api.OAuth2Token
	oauthPollErr error
}

func (f *fakeMachineLoginClient) DeviceAuthStart(context.Context) (*api.DeviceAuthStartResp, error) {
	return f.start, f.startErr
}

func (f *fakeMachineLoginClient) OAuth2DeviceStart(context.Context, string) (*api.DeviceAuthStartResp, error) {
	return f.oauthStart, f.oauthErr
}

func (f *fakeMachineLoginClient) PollUntilDone(context.Context, string, int) (*api.DeviceAuthPollResult, error) {
	return f.poll, f.pollErr
}

func (f *fakeMachineLoginClient) OAuth2PollUntilDone(context.Context, string, string, int) (*api.OAuth2Token, error) {
	return f.oauthToken, f.oauthPollErr
}

func decodeLoginMachineEvents(t *testing.T, out []byte) []loginMachineEvent {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(out))
	var events []loginMachineEvent
	for {
		var event loginMachineEvent
		if err := dec.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode JSON-lines output %q: %v", out, err)
		}
		events = append(events, event)
	}
	return events
}

func TestLoginMachineEmitsSecretFreeEventsAndPersistsCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const (
		accessToken = "management-access-secret"
		relayKey    = "sk-everyapi-relay-secret"
		deviceCode  = "device-poll-secret"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Errorf("self Authorization = %q", got)
			}
			_, _ = io.WriteString(w, `{"success":true,"data":{"id":7,"username":"alice","role":1}}`)
		case "/api/token/":
			_, _ = io.WriteString(w, `{"success":true,"data":{"items":[{"id":11,"name":"desktop","status":1,"group":""}]}}`)
		case "/api/token/11/key":
			_, _ = io.WriteString(w, `{"success":true,"data":{"key":"`+relayKey+`"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &fakeMachineLoginClient{
		start: &api.DeviceAuthStartResp{
			DeviceCode:      deviceCode,
			UserCode:        "ABCD-EFGH",
			VerificationURI: server.URL + "/verify",
			ExpiresIn:       600,
			Interval:        1,
		},
		poll: &api.DeviceAuthPollResult{
			State:       api.PollAuthorized,
			AccessToken: accessToken,
			UserID:      7,
			Username:    "alice",
		},
	}
	var out bytes.Buffer
	if err := runLoginMachine(context.Background(), server.URL, client, &out); err != nil {
		t.Fatalf("runLoginMachine: %v", err)
	}

	events := decodeLoginMachineEvents(t, out.Bytes())
	if len(events) != 2 {
		t.Fatalf("events = %#v, want verification + authorized", events)
	}
	if events[0].Version != 1 || events[0].Type != "verification" ||
		events[0].VerificationURI != server.URL+"/verify" || events[0].UserCode != "ABCD-EFGH" {
		t.Fatalf("verification event = %#v", events[0])
	}
	if events[1].Version != 1 || events[1].Type != "authorized" || events[1].Username != "alice" {
		t.Fatalf("authorized event = %#v", events[1])
	}
	for _, secret := range []string{accessToken, relayKey, deviceCode} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("stdout leaked %q: %s", secret, out.String())
		}
	}
	creds, err := config.Load()
	if err != nil {
		t.Fatalf("load persisted credentials: %v", err)
	}
	if creds.AccessToken != accessToken || creds.RelayKey != relayKey || creds.UserID != 7 || creds.Username != "alice" {
		t.Fatalf("credentials not persisted completely: %+v", creds)
	}
}

func TestLoginMachineSupportsOAuth2FallbackWithoutLeakingTokens(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const (
		accessToken  = "sk-everyapi-oauth-access-secret"
		refreshToken = "oauth-refresh-secret"
	)
	client := &fakeMachineLoginClient{
		startErr: &api.APIError{StatusCode: http.StatusNotFound, Message: "missing"},
		oauthStart: &api.DeviceAuthStartResp{
			DeviceCode:      "oauth-device-secret",
			UserCode:        "OAUTH-CODE",
			VerificationURI: "https://app.everyapi.ai/cli/auth?code=OAUTH-CODE",
			ExpiresIn:       600,
			Interval:        1,
		},
		oauthToken: &api.OAuth2Token{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    2_000_000_000,
		},
	}
	var out bytes.Buffer
	if err := runLoginMachine(context.Background(), config.DefaultAPIBase, client, &out); err != nil {
		t.Fatalf("runLoginMachine OAuth2: %v", err)
	}
	for _, secret := range []string{accessToken, refreshToken, "oauth-device-secret"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("stdout leaked %q: %s", secret, out.String())
		}
	}
	events := decodeLoginMachineEvents(t, out.Bytes())
	if len(events) != 2 || events[1].Type != "authorized" {
		t.Fatalf("events = %#v", events)
	}
	creds, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != accessToken || creds.RelayKey != accessToken || creds.RefreshToken != refreshToken {
		t.Fatalf("OAuth credentials not persisted: %+v", creds)
	}
}

func TestLoginMachineEmitsStableTerminalFailureCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{name: "expired", err: api.ErrDeviceAuthExpired, code: "expired"},
		{name: "denied", err: api.ErrDeviceAuthDenied, code: "denied"},
		{name: "cancelled", err: context.Canceled, code: "cancelled"},
		{name: "unavailable", err: errors.New("backend included sk-everyapi-server-secret"), code: "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			client := &fakeMachineLoginClient{
				start: &api.DeviceAuthStartResp{
					DeviceCode:      "device-secret",
					UserCode:        "CODE",
					VerificationURI: "https://app.everyapi.ai/cli/auth",
				},
				pollErr: tc.err,
			}
			var out bytes.Buffer
			err := runLoginMachine(context.Background(), config.DefaultAPIBase, client, &out)
			if err == nil || err.Error() != "EVERYAPI_LOGIN_ERROR:"+tc.code {
				t.Fatalf("error = %v, want stable %s code", err, tc.code)
			}
			if strings.Contains(err.Error(), "server-secret") {
				t.Fatalf("diagnostic leaked backend text: %v", err)
			}
			events := decodeLoginMachineEvents(t, out.Bytes())
			if len(events) != 2 || events[0].Type != "verification" ||
				events[1].Type != "failed" || events[1].ErrorCode != tc.code {
				t.Fatalf("events = %#v", events)
			}
		})
	}
}

func TestLoginMachineRejectsUnsafeVerificationURLBeforePolling(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	client := &fakeMachineLoginClient{
		start: &api.DeviceAuthStartResp{
			DeviceCode:      "device-secret",
			UserCode:        "CODE",
			VerificationURI: "file:///tmp/not-browsable",
		},
		poll: &api.DeviceAuthPollResult{State: api.PollAuthorized},
	}
	var out bytes.Buffer
	err := runLoginMachine(context.Background(), config.DefaultAPIBase, client, &out)
	if err == nil || err.Error() != "EVERYAPI_LOGIN_ERROR:invalid_response" {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(out.String(), "file://") || strings.Contains(out.String(), "device-secret") {
		t.Fatalf("unsafe server data leaked to stdout: %s", out.String())
	}
	events := decodeLoginMachineEvents(t, out.Bytes())
	if len(events) != 1 || events[0].Type != "failed" || events[0].ErrorCode != "invalid_response" {
		t.Fatalf("events = %#v", events)
	}
}

func TestLoginMachineModeRejectsHumanAndUnknownFlagsWithoutOutput(t *testing.T) {
	for _, args := range [][]string{
		{"--format=json-lines", "--no-browser"},
		{"--format=json-lines", "--no-qr=false"},
		{"--format=json-lines", "unexpected"},
		{"--format=json"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var out bytes.Buffer
			previous := cliout.Out
			cliout.Out = &out
			t.Cleanup(func() { cliout.Out = previous })

			err := Login(args)
			if err == nil || err.Error() != "EVERYAPI_LOGIN_ERROR:invalid_request" {
				t.Fatalf("Login(%v) error = %v", args, err)
			}
			if out.Len() != 0 {
				t.Fatalf("Login(%v) wrote stdout: %q", args, out.String())
			}
		})
	}
}
