package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const loginMachineProtocolVersion = 1

// loginMachineEvent is the complete renderer-facing login contract. It cannot represent a credential, refresh token, device code, or relay key, so those values cannot accidentally cross the CLI/desktop boundary through stdout.
type loginMachineEvent struct {
	Version         int    `json:"version"`
	Type            string `json:"type"`
	VerificationURI string `json:"verification_uri,omitempty"`
	UserCode        string `json:"user_code,omitempty"`
	Username        string `json:"username,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
}

type loginMachineError struct {
	code string
	err  error
}

// Error intentionally omits the wrapped message. Gateway errors can contain attacker-controlled text or secret-shaped values; machine consumers need a stable code, not an untrusted diagnostic payload.
func (e *loginMachineError) Error() string { return "EVERYAPI_LOGIN_ERROR:" + e.code }

func (e *loginMachineError) Unwrap() error { return e.err }

func machineLoginError(code string, err error) error {
	return &loginMachineError{code: code, err: err}
}

type loginMachineClient interface {
	DeviceAuthStart(context.Context) (*api.DeviceAuthStartResp, error)
	OAuth2DeviceStart(context.Context, string) (*api.DeviceAuthStartResp, error)
	PollUntilDone(context.Context, string, int) (*api.DeviceAuthPollResult, error)
	OAuth2PollUntilDone(context.Context, string, string, int) (*api.OAuth2Token, error)
}

// loginMachineRequested is deliberately narrow: it is used only to silence flag.FlagSet's human usage text when the caller explicitly selected the machine protocol. Login still validates the parsed value authoritatively.
func loginMachineRequested(args []string) bool {
	for i, arg := range args {
		if arg == "--format=json-lines" || arg == "-format=json-lines" {
			return true
		}
		if (arg == "--format" || arg == "-format") && i+1 < len(args) && args[i+1] == "json-lines" {
			return true
		}
	}
	return false
}

func resolveMachineLoginAPIBase(apiBaseOverride string) string {
	if strings.TrimSpace(apiBaseOverride) != "" {
		return config.ResolveAPIBase(apiBaseOverride)
	}
	// Unlike the interactive login resolver, this never opens a region picker or reads stdin. Existing settings/credentials still select the gateway.
	return config.ResolveAPIBase("")
}

func loginMachine(apiBaseOverride string) error {
	unlock, err := acquireCredentialLock()
	if err != nil {
		return machineLoginError("unavailable", fmt.Errorf("lock credential cache: %w", err))
	}
	defer unlock()

	apiBase := resolveMachineLoginAPIBase(apiBaseOverride)
	ctx, stop := cliout.SignalCtx()
	defer stop()
	return runLoginMachine(ctx, apiBase, api.New(apiBase, ""), cliout.Out)
}

func runLoginMachine(ctx context.Context, apiBase string, client loginMachineClient, out io.Writer) error {
	start, oauth2, err := startDeviceFlow(ctx, client)
	if err != nil {
		return failLoginMachine(out, "unavailable", err)
	}
	if start == nil || strings.TrimSpace(start.DeviceCode) == "" ||
		strings.TrimSpace(start.UserCode) == "" || !isDisplayableURL(start.VerificationURI) {
		return failLoginMachine(out, "invalid_response", errors.New("invalid device authorization response"))
	}
	if err := emitLoginMachineEvent(out, loginMachineEvent{
		Type:            "verification",
		VerificationURI: start.VerificationURI,
		UserCode:        start.UserCode,
	}); err != nil {
		return machineLoginError("unavailable", err)
	}

	username := ""
	if oauth2 {
		tok, pollErr := client.OAuth2PollUntilDone(ctx, oauth2CLIClientID, start.DeviceCode, start.Interval)
		if pollErr != nil {
			code := loginMachinePollErrorCode(pollErr)
			return failLoginMachine(out, code, pollErr)
		}
		if tok == nil || strings.TrimSpace(tok.AccessToken) == "" {
			return failLoginMachine(out, "invalid_response", errors.New("empty OAuth credential"))
		}
		if _, saveErr := saveOAuth2LoginCredentials(apiBase, tok); saveErr != nil {
			return failLoginMachine(out, "credential_store", saveErr)
		}
	} else {
		res, pollErr := client.PollUntilDone(ctx, start.DeviceCode, start.Interval)
		if pollErr != nil {
			code := loginMachinePollErrorCode(pollErr)
			return failLoginMachine(out, code, pollErr)
		}
		if res == nil || res.State != api.PollAuthorized || strings.TrimSpace(res.AccessToken) == "" {
			return failLoginMachine(out, "invalid_response", errors.New("invalid authorization result"))
		}
		creds, saveErr := saveLegacyLoginCredentials(ctx, apiBase, res)
		if saveErr != nil {
			return failLoginMachine(out, "credential_store", saveErr)
		}
		username = cliout.Sanitize(res.Username)
		// Match interactive login's eager cache fill. Relay-key lookup remains non-fatal: authentication succeeded and the next credential request can retry a transient failure or report that the account has no key.
		_, _ = api.ResolveRelayKey(ctx, creds, "")
	}

	if err := emitLoginMachineEvent(out, loginMachineEvent{
		Type:     "authorized",
		Username: username,
	}); err != nil {
		return machineLoginError("unavailable", err)
	}
	return nil
}

func loginMachinePollErrorCode(err error) string {
	switch {
	case errors.Is(err, api.ErrDeviceAuthExpired):
		return "expired"
	case errors.Is(err, api.ErrDeviceAuthDenied):
		return "denied"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	default:
		return "unavailable"
	}
}

func failLoginMachine(out io.Writer, code string, cause error) error {
	if err := emitLoginMachineEvent(out, loginMachineEvent{Type: "failed", ErrorCode: code}); err != nil {
		return machineLoginError("unavailable", err)
	}
	return machineLoginError(code, cause)
}

func emitLoginMachineEvent(out io.Writer, event loginMachineEvent) error {
	event.Version = loginMachineProtocolVersion
	if err := json.NewEncoder(out).Encode(event); err != nil {
		return fmt.Errorf("write login event: %w", err)
	}
	if flusher, ok := out.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return fmt.Errorf("flush login event: %w", err)
		}
	}
	return nil
}
