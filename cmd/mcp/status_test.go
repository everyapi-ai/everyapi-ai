package mcp

import (
	"os/exec"
	"testing"
)

// TestProbeClient drives the three real branches of probeClient against /bin/echo as a stand-in for the per-client binary. echo is always-on-PATH and its argv is its stdout, so we can shape the "registered" vs "not registered" outcome by what we pass.
//
// The exec timeout path is harder to exercise cheaply — covered by code review (5s ceiling, exec.CommandContext) rather than a flaky sleep-based test.
func TestProbeClient(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("/bin/echo not on PATH; can't run probe test")
	}

	t.Run("not on PATH", func(t *testing.T) {
		c := &mcpClient{
			Name:     "definitely-not-a-real-binary-name-xyz",
			ListArgv: func() []string { return []string{"mcp", "list"} },
		}
		if got := probeClient(c); got != "not on PATH" {
			t.Errorf("got %q, want 'not on PATH'", got)
		}
	})

	t.Run("registered when output mentions everyapi", func(t *testing.T) {
		c := &mcpClient{
			Name:     "echo",
			ListArgv: func() []string { return []string{"everyapi mcp server-1 healthy"} },
		}
		if got := probeClient(c); got != "registered" {
			t.Errorf("got %q, want 'registered'", got)
		}
	})

	t.Run("not registered when output omits everyapi", func(t *testing.T) {
		c := &mcpClient{
			Name:     "echo",
			ListArgv: func() []string { return []string{"some other server"} },
		}
		if got := probeClient(c); got != "not registered" {
			t.Errorf("got %q, want 'not registered'", got)
		}
	})

	t.Run("not registered when everyapi appears only in another server command", func(t *testing.T) {
		c := &mcpClient{
			Name:     "echo",
			ListArgv: func() []string { return []string{"foo /usr/bin/everyapi-helper enabled"} },
		}
		if got := probeClient(c); got != "not registered" {
			t.Errorf("got %q, want 'not registered'", got)
		}
	})

	t.Run("registered in Gemini glyph-prefixed list", func(t *testing.T) {
		c := &mcpClient{
			Name:     "echo",
			ListArgv: func() []string { return []string{"○ everyapi: everyapi mcp (stdio)"} },
		}
		if got := probeClient(c); got != "registered" {
			t.Errorf("got %q, want 'registered'", got)
		}
	})

	t.Run("missing ListArgv → probe failed", func(t *testing.T) {
		// Defensive: an entry with no list verb shouldn't blow up.
		c := &mcpClient{Name: "echo", ListArgv: nil}
		if got := probeClient(c); got != "(probe failed)" {
			t.Errorf("got %q, want '(probe failed)'", got)
		}
	})

	t.Run("targeted probe treats clean not-found exit as not registered", func(t *testing.T) {
		c := &mcpClient{
			Name: "sh",
			ProbeArgv: func(string) []string {
				return []string{"-c", `echo 'No MCP server named "everyapi".' >&2; exit 1`}
			},
		}
		if got := probeClient(c); got != "not registered" {
			t.Errorf("got %q, want 'not registered'", got)
		}
	})

	t.Run("targeted probe preserves real failures", func(t *testing.T) {
		c := &mcpClient{
			Name: "sh",
			ProbeArgv: func(string) []string {
				return []string{"-c", `echo 'Configuration file is corrupted' >&2; exit 1`}
			},
		}
		if got := probeClient(c); got != "(probe failed)" {
			t.Errorf("got %q, want '(probe failed)'", got)
		}
	})
}

func TestListOutputHasServerPreservesRows(t *testing.T) {
	codex := []byte("Name      Command   Args  Env  Cwd  Status   Auth\n" +
		"everyapi  everyapi  mcp   -    -    enabled  Unsupported\n")
	if !listOutputHasServer(codex, "everyapi") {
		t.Fatal("did not find everyapi in Codex header-plus-row output")
	}

	nearName := []byte("Name  Command\nfoo   /usr/bin/everyapi-helper\n")
	if listOutputHasServer(nearName, "everyapi") {
		t.Fatal("matched everyapi only inside another server's command")
	}
}
