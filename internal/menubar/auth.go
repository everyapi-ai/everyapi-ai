package menubar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"runtime"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// runDeviceAuth executes the full device-authorization flow as kicked
// off by the user clicking "Sign in…". Unlike cmd/login.go it has no
// terminal output — the caller (controller) is responsible for
// surfacing the user_code in the menu and reporting completion via
// notifications + state transitions.
//
// Lifecycle:
//  1. POST /api/cli/device-auth-start → user_code + verification URL
//  2. menubarUI(userCode) callback so the controller can update the
//     menu BEFORE the browser opens (avoids a race where the user
//     clicks 'Sign in', the browser pops, but the menu still shows
//     "Sign in…")
//  3. Open the browser at the prefilled URL (?code=…)
//  4. Poll until authorized / expired / denied / canceled
//  5. On success persist credentials to ~/.config/everyapi via the
//     same config.Save the CLI uses — both surfaces share one creds
//     file by design (status from one, sign-out from the other)
//
// On any failure the function returns; the controller decides what
// state to revert to (typically StateLoggedOut). The returned creds
// are nil on error.
func runDeviceAuth(ctx context.Context, apiBase string, menubarUI func(userCode string)) (*config.Credentials, error) {
	client := api.New(apiBase, "")

	start, err := client.DeviceAuthStart(ctx)
	if err != nil {
		return nil, fmt.Errorf("device-auth-start: %w", err)
	}

	prefilled := buildVerificationURLWithCode(start.VerificationURI, start.UserCode)
	if menubarUI != nil {
		menubarUI(start.UserCode)
	}

	if err := openBrowser(prefilled); err != nil {
		// Browser open failing is not fatal — the user can still
		// approve manually if they have the URL/code in front of
		// them. Log for diagnostics.
		log.Printf("menubar: could not auto-open browser: %v", err)
	}

	res, err := client.PollUntilDone(ctx, start.DeviceCode, start.Interval)
	if err != nil {
		switch {
		case errors.Is(err, api.ErrDeviceAuthExpired):
			return nil, fmt.Errorf("authorization timed out — try again")
		case errors.Is(err, api.ErrDeviceAuthDenied):
			return nil, fmt.Errorf("authorization denied")
		default:
			return nil, fmt.Errorf("poll: %w", err)
		}
	}

	creds := &config.Credentials{
		APIBase:     apiBase,
		AccessToken: res.AccessToken,
		UserID:      res.UserID,
		Username:    res.Username,
	}
	if err := config.Save(creds); err != nil {
		return nil, fmt.Errorf("save credentials: %w", err)
	}
	return creds, nil
}

// openBrowser launches the platform default browser at url. Mirrors
// cliprompt.OpenBrowser but the menubar package cannot import
// cliprompt (it pulls in tty-prompting code we don't need); a fresh
// helper here is the lighter dependency.
//
// Exposed as a package var so tests can stub the shellout — the
// indirection is free at runtime.
var openBrowser = realOpenBrowser

func realOpenBrowser(targetURL string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", targetURL).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL).Start()
	default:
		return exec.Command("xdg-open", targetURL).Start()
	}
}
