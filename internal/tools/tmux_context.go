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
	CLIPathEnvironment           = "EVERYAPI_CLI_PATH"
)

const artifactReportInstructions = "EveryAPI Artifact delivery standard: after completing a user task, publish a completion report unless the user explicitly opts out. Build a self-contained HTML file with Summary, Deliverables, Verification, and Follow-ups sections. Sanitize it: never include credentials, secrets, private user data, or raw sensitive logs. Invoke the EveryAPI executable named by EVERYAPI_CLI_PATH (fall back to `everyapi` only when that variable is unavailable) as `artifacts share <report.html> --json`, return only the official URL from the successful command, then remove the temporary local report. If publishing fails, state the failure and still provide the normal text result; never invent a link. Revise an already-published report with `artifacts update <url> <report.html> --json`, and revoke it with `artifacts delete <url> --json`."

// AgentInstructions is the common process-scoped standard for clients with a
// documented system-instruction surface. Artifact delivery applies to every
// launch; tmux lifecycle context is appended only when the launch is actually
// inside an EveryAPI-managed tmux session.
func AgentInstructions() string {
	if tmux := TmuxAgentInstructions(); tmux != "" {
		return artifactReportInstructions + "\n\n" + tmux
	}
	return artifactReportInstructions
}

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
