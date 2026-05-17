package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/relaya-ai/relaya-ai/internal/api"
	"github.com/relaya-ai/relaya-ai/internal/config"
)

// Login runs the device authorization flow: POST start → render
// user_code + URL → open browser → poll until authorized → write
// credentials.
//
// Flags:
//
//	--api-base <url>  override default https://api.relaya.pro (dev / self-host)
//	--no-browser      skip the auto-open; user copies the URL manually
func Login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	apiBase := fs.String("api-base", config.DefaultAPIBase, "Relaya API base URL")
	noBrowser := fs.Bool("no-browser", false, "skip opening the browser automatically")
	if err := fs.Parse(args); err != nil {
		return err
	}
	*apiBase = strings.TrimRight(*apiBase, "/")

	client := api.New(*apiBase, "")
	ctx := withCtx()

	start, err := client.DeviceAuthStart(ctx)
	if err != nil {
		return fmt.Errorf("start device authorization: %w", err)
	}

	println("")
	println("To authorize this device, visit:")
	printf("\n    %s\n\n", start.VerificationURI)
	println("And enter the code:")
	printf("\n    %s\n\n", start.UserCode)

	if !*noBrowser {
		if err := openBrowser(start.VerificationURI); err == nil {
			println("Browser opened. Approve there and come back; this will finish on its own.")
		} else {
			// stderr so a user piping `relaya login | …` gets a clean
			// stdout (the URL + code go through the cmd.Out writer
			// above). xdg-open missing on a headless Linux desktop is
			// the common case here.
			fmt.Fprintln(os.Stderr, "Couldn't open the browser automatically — copy the URL above.")
		}
	}
	println("")
	println("Waiting for authorization...")

	res, err := client.PollUntilDone(ctx, start.DeviceCode, start.Interval)
	if err != nil {
		switch err {
		case api.ErrDeviceAuthExpired:
			return fmt.Errorf("the code timed out before you authorized — run 'relaya login' again")
		case api.ErrDeviceAuthDenied:
			return fmt.Errorf("authorization was denied in the browser")
		default:
			return fmt.Errorf("poll: %w", err)
		}
	}

	creds := &config.Credentials{
		APIBase:     *apiBase,
		AccessToken: res.AccessToken,
		UserID:      res.UserID,
		Username:    res.Username,
	}
	if err := config.Save(creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	dir, _ := config.ConfigDir()
	printf("\nLogged in as %s. Credentials saved to %s/credentials.json\n", res.Username, dir)

	// Resolve the relay API key now (and cache it) so `relaya use`
	// works on first try. The access token alone can't relay — it's
	// a management credential — so without this step `use` would
	// 401.
	//
	// A failure here is non-fatal: login itself already succeeded and
	// credentials are saved. We only surface the errNoRelayKey
	// sentinel (account has zero enabled keys — an actionable state
	// the user must fix in the dashboard). Other failures (transient
	// 5xx, network blip) are SWALLOWED: after a successful device-auth
	// flow, a noisy "warning: ..." line on stderr would make the user
	// doubt the login itself, and the next `relaya use` / `relaya
	// status` will retry the resolution anyway.
	if _, err := resolveRelayKey(creds); err != nil && errors.Is(err, errNoRelayKey) {
		println("")
		println("Note: your account has no relay API key yet. `relaya use` needs one")
		println("(it's separate from this login token). Create an API key in the")
		println("Relaya dashboard, then run 'relaya login' again.")
	}

	println("Next: try 'relaya status' or 'relaya use claude'.")
	return nil
}

// openBrowser tries the platform's standard "open URL" helper. We
// intentionally use exec.Command + ignore stderr: a browser launcher
// that fails should not look like a CLI bug — we already print the URL
// for the user to copy.
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}
