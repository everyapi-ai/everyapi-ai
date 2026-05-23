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
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(adminUsage)
		if len(args) == 0 {
			return fmt.Errorf(i18n.T("common.missing_subcommand"), "everyapi admin")
		}
		return nil
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "marketplace":
		return adminMarketplace(rest)
	case "user":
		return adminUser(rest)
	case "channel":
		return adminChannel(rest)
	case "log":
		return adminLog(rest)
	case "abuse":
		return adminAbuse(rest)
	case "audit":
		return adminAudit(rest)
	case "redemption":
		return adminRedemption(rest)
	default:
		cliout.Printf("%s\n", adminUsage)
		return fmt.Errorf(i18n.T("common.unknown_subcommand"), "admin", sub)
	}
}

const adminUsage = `everyapi admin — operator commands (admin-only)

USAGE
  everyapi admin <subcommand> [flags]

SUBCOMMANDS
  marketplace status     Print the current marketplace.enabled flag
  marketplace on         PUT /api/option/ marketplace.enabled=true  (open BYO-GPU + seller flow)
  marketplace off        PUT /api/option/ marketplace.enabled=false (close them; existing nodes keep running)
  user search <keyword>  Fuzzy lookup over username / email / display name
  user show <id>         Single user record
  user list [--page P]   Paged user list
  user manage <id> --action enable|disable|delete|promote_admin|demote_admin
                         POST /api/user/manage — backend enforces role check
  channel test <id>      Health-check one channel
  channel tag <name> --enable|--disable
                         Bulk-flip every channel carrying the tag
  log tail [--user U] [--model M] [--channel C] [--group G] [--since W]
                         Paged admin log search
  abuse list [--status]  Filed reports
  abuse show <id>        Single report
  abuse update <id> --status X [--note N]
                         Triage a report
  audit [--page P]       Recent admin audit-log rows
  redemption list|search <kw>|show <id>|create --name N --quota Q [--count C] [--expires E]
             |update <id>|status <id> enable|disable|delete <id>|clear-invalid
                         Manage prepaid quota voucher codes (create mints + prints keys)
  help                   Print this message.

Auth: re-uses your 'everyapi login' token. You must be an admin on
the backend — non-admin tokens get a 403 with the backend's stock
unauthorized message.

Common audit trail: marketplace toggle commits to the same Option
table the dashboard panel writes to, so the existing audit-log path
captures both UI and cli toggles uniformly.
`
