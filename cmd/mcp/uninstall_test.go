package mcp

import "testing"

func TestIsNotRegistered_GeminiZeroExitMessage(t *testing.T) {
	for _, message := range []string{
		`Server "everyapi" not found in user settings.`,
		`No MCP server named 'everyapi' found.`,
		`No MCP server named "everyapi". Run claude mcp add to add one.`,
		`No MCP server found with name: everyapi`,
	} {
		if !isNotRegistered(message) {
			t.Errorf("not-found message was not recognized: %q", message)
		}
	}
	if isNotRegistered(`Profile "work" not found while removing everyapi.`) {
		t.Fatal("unrelated not-found error must not be swallowed")
	}
}
