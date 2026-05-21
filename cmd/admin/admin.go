// Package admin holds `everyapi admin ...`, the operator-side
// subcommands gated by the backend's middleware.AdminAuth. The single
// surface this PR adds is `admin marketplace {status|on|off}` — a
// fast cli toggle for the marketplace.enabled flag so ops doesn't
// have to round-trip through the dashboard panel for a 30-second
// open-close-window during testing or maintenance.
//
// Subcommands:
//
//	everyapi admin marketplace status     Print current marketplace.enabled
//	everyapi admin marketplace on         PUT /api/option/ key=marketplace.enabled value=true
//	everyapi admin marketplace off        PUT /api/option/ key=marketplace.enabled value=false
//	everyapi admin help
//
// Auth: the same `sk-everyapi-` access token from 'everyapi login' —
// the user must already be an admin on the backend (role >=
// RoleAdminUser); non-admin tokens get a 403 with the backend's
// stock "unauthorized" message.
package admin

import (
	"errors"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(adminUsage)
		if len(args) == 0 {
			return errors.New("missing subcommand (try 'everyapi admin help')")
		}
		return nil
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "marketplace":
		return adminMarketplace(rest)
	default:
		cliout.Printf("%s\n", adminUsage)
		return fmt.Errorf("unknown 'admin' subcommand %q", sub)
	}
}

const adminUsage = `everyapi admin — operator commands (admin-only)

USAGE
  everyapi admin <subcommand> [flags]

SUBCOMMANDS
  marketplace status     Print the current marketplace.enabled flag
  marketplace on         PUT /api/option/ marketplace.enabled=true  (open BYO-GPU + seller flow)
  marketplace off        PUT /api/option/ marketplace.enabled=false (close them; existing nodes keep running)
  help                   Print this message.

Auth: re-uses your 'everyapi login' token. You must be an admin on
the backend — non-admin tokens get a 403 with the backend's stock
unauthorized message.

Common audit trail: marketplace toggle commits to the same Option
table the dashboard panel writes to, so the existing audit-log path
captures both UI and cli toggles uniformly.
`
