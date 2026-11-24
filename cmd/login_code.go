package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
)

// cliAuthCodePath is the dashboard page that generates one-time CLI authorization codes (web/apps/dashboard/src/routes/_app/cli/code.tsx).
const cliAuthCodePath = "/cli/code"

// cliAuthCodeAttempts bounds how many rejected codes an interactive login accepts before giving up. Off a TTY there is no one to retype, so a single attempt is made.
const cliAuthCodeAttempts = 3

// loginCodeInteractive decides whether a rejected code may be retyped; a variable so tests can exercise the retry loop.
var loginCodeInteractive = cliprompt.IsInteractive

type cliAuthCodeRedeemer interface {
	RedeemCLIAuthCode(context.Context, string) (*api.DeviceAuthPollResult, error)
}

// loginWithAuthCode is the default `auth login` path: point the user at the dashboard page that issues a one-time code, read the code, redeem it, and store the resulting management credential exactly as an approved device flow would.
func loginWithAuthCode(ctx context.Context, apiBase, alias string, client cliAuthCodeRedeemer, readCode func() (string, error)) error {
	codeURL := strings.TrimRight(api.WebOriginFromBase(apiBase), "/") + cliAuthCodePath
	cliout.Println("")
	cliout.Println(i18n.T("login.code_get_hint"))
	cliout.Printf("\n    %s\n\n", cliout.Sanitize(codeURL))

	attempts := 1
	if loginCodeInteractive() {
		attempts = cliAuthCodeAttempts
	}
	for i := 0; i < attempts; i++ {
		code, err := readCodeWithContext(ctx, readCode)
		if err != nil {
			if errors.Is(err, cliprompt.ErrPickCancelled) || errors.Is(err, context.Canceled) {
				cliout.Println(i18n.T("login.cancelled"))
				return nil
			}
			if errors.Is(err, io.EOF) {
				return errors.New(i18n.T("login.code_missing"))
			}
			return err
		}
		code = strings.TrimSpace(code)
		if code == "" {
			return errors.New(i18n.T("login.code_missing"))
		}
		res, err := client.RedeemCLIAuthCode(ctx, code)
		if err != nil {
			var invalid *api.ErrCLIAuthCodeInvalid
			if errors.As(err, &invalid) {
				rejected := fmt.Sprintf(i18n.T("login.code_rejected"), cliout.Sanitize(invalid.Message))
				// Off a TTY the single attempt is the whole budget, so "too many invalid codes" would misreport what happened.
				if attempts == 1 {
					return errors.New(rejected)
				}
				fmt.Fprintln(os.Stderr, rejected)
				continue
			}
			var ae *api.APIError
			if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
				return errors.New(i18n.T("login.code_unsupported"))
			}
			if errors.Is(err, context.Canceled) {
				cliout.Println(i18n.T("login.cancelled"))
				return nil
			}
			return fmt.Errorf("redeem authorization code: %w", err)
		}
		return finishLegacyLogin(ctx, apiBase, alias, res)
	}
	return errors.New(i18n.T("login.code_attempts_exhausted"))
}

// readCodeWithContext returns as soon as ctx is cancelled even while readCode is still blocked. SignalCtx turns Ctrl+C into a context cancellation instead of killing the process, and a plain line read on a terminal stdin (stdout redirected, so the huh prompt is not used) never observes that cancellation, which would leave the login unkillable. The abandoned read is reaped when the process exits.
func readCodeWithContext(ctx context.Context, readCode func() (string, error)) (string, error) {
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := readCode()
		done <- result{code, err}
	}()
	select {
	case r := <-done:
		return r.code, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// cliAuthCodeStdin is shared across prompts so a piped stdin carrying several lines is consumed one line per attempt instead of being swallowed by the first reader's buffer.
var cliAuthCodeStdin = bufio.NewReader(os.Stdin)

func readCLIAuthCode() (string, error) {
	return cliprompt.Line(cliAuthCodeStdin, i18n.T("login.code_prompt"), "")
}
