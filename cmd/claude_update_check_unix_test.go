//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunClaudeUpdateSurvivesUpdaterInterrupt(t *testing.T) {
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	script := "#!/bin/sh\nkill -INT \"$PPID\"\nsleep 0.1\nexit 17\n"
	if err := os.WriteFile(claudePath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestRunClaudeUpdateInterruptHelper$")
	child.Env = append(os.Environ(),
		"EVERYAPI_CLAUDE_UPDATE_INTERRUPT_HELPER=1",
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("EveryAPI died with the interrupted updater: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "parent survived updater interrupt") {
		t.Fatalf("missing survival marker in helper output:\n%s", out)
	}
}

func TestRunClaudeUpdateInterruptHelper(t *testing.T) {
	if os.Getenv("EVERYAPI_CLAUDE_UPDATE_INTERRUPT_HELPER") != "1" {
		return
	}
	if err := runClaudeUpdate(); err == nil {
		t.Fatal("interrupted updater unexpectedly succeeded")
	}
	fmt.Println("parent survived updater interrupt")
}
