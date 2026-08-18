package tools

import (
	"fmt"
	"os"
	"strings"
)

const (
	TerminalModeEnvironment      = "EVERYAPI_TERMINAL_MODE"
	TmuxSessionEnvironment       = "EVERYAPI_TMUX_SESSION"
	TmuxAttachCommandEnvironment = "EVERYAPI_TMUX_ATTACH_COMMAND"
)

// TmuxAgentInstructions returns process-scoped context only for a verified tmux launch. The environment variables are public integration points for every client; clients with a documented instruction surface also receive this text proactively.
func TmuxAgentInstructions() string {
	if os.Getenv("TMUX") == "" || os.Getenv(TerminalModeEnvironment) != "tmux" {
		return ""
	}
	session := os.Getenv(TmuxSessionEnvironment)
	if session == "" {
		return ""
	}
	attachCommand := os.Getenv(TmuxAttachCommandEnvironment)
	if attachCommand == "" {
		attachCommand = TmuxAttachCommand(session)
	}
	return fmt.Sprintf("You are running inside tmux session %s for an EveryAPI tmux-mode launch. This session can continue running after the user detaches. Do not create a nested tmux session. If the user needs to reconnect, tell them to run `%s` from a terminal outside tmux.", session, attachCommand)
}

func TmuxAttachCommand(session string) string {
	if session != "" && strings.IndexFunc(session, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) == -1 {
		return "tmux attach -t " + session
	}
	return "tmux attach -t '" + strings.ReplaceAll(session, "'", `'"'"'`) + "'"
}
