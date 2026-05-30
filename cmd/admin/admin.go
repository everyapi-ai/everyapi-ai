// Package admin holds `everyapi admin ...`, the operator-side
// subcommands gated by the backend's middleware.AdminAuth. It spans
// seven areas — marketplace, user, channel, log, abuse, audit and
// redemption (see the admin.help.usage locale text for the verb list).
//
// Two entry points share one dispatch:
//   - typed: `everyapi admin <area> <action> [flags]` → Run's switch.
//   - interactive: bare `everyapi admin` on a TTY → runConsole (see
//     console.go), a two-level area→action picker that prompts inline
//     for any value an action needs and then feeds the same Run switch.
//
// Auth: the same `sk-everyapi-` access token from 'everyapi auth login' —
// the user must already be an admin on the backend (role >=
// RoleAdminUser); non-admin tokens get a 403 with the backend's
// stock "unauthorized" message.
package admin

import (
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func Run(args []string) error {
	// Bare `everyapi admin` on a TTY launches the interactive operator
	// console (area → action → inline prompts). Non-TTY (scripts/CI) keeps
	// the old "missing subcommand" usage so piped callers don't hang on a
	// picker.
	if len(args) == 0 && cliprompt.IsInteractive() {
		return runConsole()
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("admin.help.usage"))
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
		cliout.Println(i18n.T("admin.help.usage"))
		return fmt.Errorf(i18n.T("common.unknown_subcommand"), "admin", sub)
	}
}
