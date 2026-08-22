package cmd

import (
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

const tmuxUsageKey = "tmux.usage"

// TmuxSessions dispatches `everyapi tmux`. It is deliberately login-free and
// network-free: every session it reports already exists on this machine, and a
// user who cannot reach the API still needs to get back into a running agent.
func TmuxSessions(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			cliout.Println(i18n.T(tmuxUsageKey))
			return nil
		}
	}
	if len(args) == 0 {
		return runTmuxDefault()
	}
	switch args[0] {
	case "list":
		return runTmuxList(args[1:])
	case "attach":
		return runTmuxAttach(args[1:])
	case "kill":
		return runTmuxKill(args[1:])
	default:
		return fmt.Errorf("unknown 'tmux' subcommand %q — try `everyapi tmux help`", args[0])
	}
}
