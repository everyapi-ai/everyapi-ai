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
