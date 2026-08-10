package cmd

import (
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

// Auth dispatches `everyapi auth <sub>`. login / logout / status used to
// be top-level commands; they now live under `auth` so the launcher's
// top level isn't crowded with session plumbing. The subcommands keep
// their own flag parsing — Auth just routes args[1:] to them. In particular,
// `auth login --format=json-lines` is the non-interactive desktop sidecar
// protocol; Auth must not add output or prompting around it.
//
// Bare `everyapi auth` on a TTY never reaches here with empty args (the
// launcher's sub-picker handles it); off a TTY it prints usage. `help`
// and an unknown subcommand both print usage, the latter with an error.
func Auth(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("auth.usage"))
		return nil
	}
	switch args[0] {
	case "login":
		return Login(args[1:])
	case "logout":
		return Logout(args[1:])
	case "status":
		return Status(args[1:])
	case "credential":
		return Credential(args[1:])
	case "avatar":
		return AvatarMachine(args[1:])
	default:
		cliout.Println(i18n.T("auth.usage"))
		return fmt.Errorf(i18n.T("auth.unknown_sub"), args[0])
	}
}
