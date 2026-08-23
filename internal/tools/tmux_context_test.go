package tools

import (
	"strings"
	"testing"
)

func TestAgentInstructionsAlwaysCarryArtifactDeliveryStandard(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(TerminalModeEnvironment, "native")

	instructions := AgentInstructions()
	for _, required := range []string{
		"EveryAPI Artifact delivery standard",
		"Summary, Deliverables, Verification, and Follow-ups",
		"EVERYAPI_CLI_PATH",
		"artifacts share <report.html> --json",
		"never invent a link",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("agent instructions missing %q: %s", required, instructions)
		}
	}
	if strings.Contains(instructions, "inside tmux session") {
		t.Fatalf("native agent instructions contain tmux context: %s", instructions)
	}
}

func TestAgentInstructionsAppendVerifiedTmuxContext(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv(TerminalModeEnvironment, "tmux")
	t.Setenv(TmuxSessionEnvironment, "everyapi-report-test")
	t.Setenv(TmuxAttachCommandEnvironment, "tmux attach -t everyapi-report-test")

	instructions := AgentInstructions()
	if !strings.Contains(instructions, "EveryAPI Artifact delivery standard") ||
		!strings.Contains(instructions, "inside tmux session everyapi-report-test") {
		t.Fatalf("combined agent instructions = %s", instructions)
	}
}

// TestAgentInstructionsListRunnableCapabilities covers the reason this earns system-prompt budget at all: an agent that knows a command exists runs it, and one that does not asks the user to go look it up or answers from training data this platform is not in.
func TestAgentInstructionsListRunnableCapabilities(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(TerminalModeEnvironment, "native")

	instructions := AgentInstructions()
	for _, required := range []string{
		"EVERYAPI_CLI_PATH",
		"docs list", "docs <topic>", "docs search <query>",
		"auth status",
		"stats usage", "stats log stat", "stats log summary", "stats log list",
		"models list", "models pricing",
		"stats perf", "stats upstream", "doctor",
		"token list",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("capability list is missing %q: %s", required, instructions)
		}
	}
	// Without the scoping sentence a model told to consult EveryAPI's documentation reaches for it on questions about the user's own project.
	if !strings.Contains(instructions, "not the user's own project") {
		t.Errorf("capability list does not scope itself to EveryAPI: %s", instructions)
	}
}

// TestAgentInstructionsFenceOffStateChangingCommands is the safety property, and the one worth breaking a build over. `everyapi` is not a read-only tool — token revoke, wallet topup, seller withdraw, and edge remove move money and destroy access — so an agent handed the CLI as a uniform information source will eventually run one of them to "check" something. The read-only set has to be enumerated, everything else declared state-changing, and `token key` called out on its own: it changes nothing and still prints a credential.
func TestAgentInstructionsFenceOffStateChangingCommands(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(TerminalModeEnvironment, "native")

	instructions := AgentInstructions()
	if !strings.Contains(instructions, "Read-only") {
		t.Error("the safe set is not marked read-only")
	}
	for _, required := range []string{
		"Every other subcommand changes state",
		"explicit yes",
		"token key <id>",
		"plaintext",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("state-changing fence is missing %q: %s", required, instructions)
		}
	}
	// A command that moves money or destroys access must never appear in the runnable list.
	safeSet := instructions[strings.Index(instructions, "Read-only"):strings.Index(instructions, "Every other subcommand")]
	for _, forbidden := range []string{
		"token revoke", "token create", "wallet topup", "wallet redeem",
		"seller withdraw", "seller add-key", "edge remove", "edge start",
		"checkin claim", "auth logout", "artifacts delete", "aff transfer",
	} {
		if strings.Contains(safeSet, forbidden) {
			t.Errorf("%q is listed as safe to run unprompted:\n%s", forbidden, safeSet)
		}
	}
}

// TestAgentInstructionsOrderPutsCapabilitiesFirst: "you can find this out yourself" applies to most turns; "publish a report" applies once at the end of a task.
func TestAgentInstructionsOrderPutsCapabilitiesFirst(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(TerminalModeEnvironment, "native")

	instructions := AgentInstructions()
	capabilities := strings.Index(instructions, "EveryAPI CLI")
	artifact := strings.Index(instructions, "EveryAPI Artifact delivery standard")
	if capabilities < 0 || artifact < 0 {
		t.Fatalf("a section is missing: %s", instructions)
	}
	if capabilities > artifact {
		t.Errorf("artifact standard precedes the capability list")
	}
}

// TestAgentInstructionsSectionsStaySeparated keeps a blank line in front of each section. Three standards run together as one paragraph read as one rule with contradictory clauses — and counting blank lines will not do it, since the capability list has them inside it. Check the boundary in front of each section instead.
func TestAgentInstructionsSectionsStaySeparated(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv(TerminalModeEnvironment, "tmux")
	t.Setenv(TmuxSessionEnvironment, "everyapi-order-test")
	t.Setenv(TmuxAttachCommandEnvironment, "tmux attach -t everyapi-order-test")

	instructions := AgentInstructions()
	for _, section := range []string{
		"EveryAPI Artifact delivery standard",
		"You are running inside tmux session",
	} {
		at := strings.Index(instructions, section)
		if at < 0 {
			t.Errorf("section %q is missing", section)
			continue
		}
		if at < 2 || instructions[at-2:at] != "\n\n" {
			t.Errorf("section %q is not preceded by a blank line", section)
		}
	}
	// The first section opens the text; nothing may precede it.
	if !strings.HasPrefix(instructions, "EveryAPI CLI") {
		t.Errorf("capability list is not first: %s", instructions)
	}
}
