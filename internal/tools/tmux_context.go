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

// cliCapabilityInstructions tells a launched agent what the EveryAPI binary can answer, and — the part that actually needed care — which half of it the agent may run on its own.
//
// Why the capability list and not just a pointer at the docs: an agent that knows a command exists uses it, and an agent that does not asks the user to go look it up, or worse answers from recalled training data. "How much did this session cost" and "which models can this key reach" are questions the binary already answers exactly, on a machine where it is already installed and already authenticated.
//
// The read-only / state-changing split is the whole safety design. `everyapi` is not a read-only tool: `token revoke`, `wallet topup`, `seller withdraw`, and `edge remove` move money and destroy access, and an agent that treats the CLI as a uniform information source will eventually run one of them to "check" something. So the safe set is enumerated explicitly rather than described, everything outside it is declared state-changing, and `token key` is called out separately — it changes nothing but prints a credential in plaintext, which no summary of "read-only" would have caught.
//
// The scoping sentence is not filler either. Without it a model told to consult EveryAPI's documentation reaches for it on questions about the user's own project, which it says nothing about.
const cliCapabilityInstructions = `EveryAPI CLI: the executable named by EVERYAPI_CLI_PATH (fall back to ` + "`everyapi`" + ` only when that variable is unavailable) answers questions about this gateway and this account directly. Prefer running it over answering from recalled knowledge — which is frequently wrong or out of date for this platform — and over asking the user to go look something up.

Read-only. Run these yourself, unprompted, whenever one would answer the question:
  docs list | docs <topic> | docs search <query>  the handbook, offline
  auth status                                     identity, quota, usage
  stats usage | stats log stat                    consumption and spend
  stats log summary | stats log list              per-model spend, recent logs
  models list | models pricing                    routable models and rates
  stats perf | stats upstream | doctor            model, provider, local health
  token list                                      this account's keys, masked

The documentation covers EveryAPI, not the user's own project; do not reach for it on unrelated questions.

Every other subcommand changes state — it moves money, revokes keys, mounts channels, removes nodes. Name the exact command and get an explicit yes from the user before running one. ` + "`token key <id>`" + ` changes nothing but prints a key in plaintext: run it only when the user asked for that key.`

const artifactReportInstructions = "EveryAPI Artifact delivery standard: after completing a user task, publish a completion report unless the user explicitly opts out. Build a self-contained HTML file with Summary, Deliverables, Verification, and Follow-ups sections. Sanitize it: never include credentials, secrets, private user data, or raw sensitive logs. Invoke the EveryAPI executable named by EVERYAPI_CLI_PATH (fall back to `everyapi` only when that variable is unavailable) as `artifacts share <report.html> --json`, return only the official URL from the successful command, then remove the temporary local report. If publishing fails, state the failure and still provide the normal text result; never invent a link. Revise an already-published report with `artifacts update <url> <report.html> --json`, and revoke it with `artifacts delete <url> --json`."

// AgentInstructions is the common process-scoped standard for clients with a
// documented system-instruction surface. The capability list and artifact
// delivery apply to every launch; tmux lifecycle context is appended only when
// the launch is actually inside an EveryAPI-managed tmux session.
//
// Capabilities first, deliberately: "you can find this out yourself" applies to
// most turns, while "publish a report" applies once at the end of a task.
func AgentInstructions() string {
	sections := []string{cliCapabilityInstructions, artifactReportInstructions}
	if tmux := TmuxAgentInstructions(); tmux != "" {
		sections = append(sections, tmux)
	}
	return strings.Join(sections, "\n\n")
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
