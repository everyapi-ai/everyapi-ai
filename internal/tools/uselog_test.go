package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestLogToolExitWritesLine verifies a diagnostic line lands in <config>/use.log with the pid, tool, and cause, and that a second call appends rather than overwrites. Config dir is redirected via XDG_CONFIG_HOME so the test never touches the real ~/.config/everyapi.
func TestLogToolExitWritesLine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	logToolExit("claude", 4242, "killed by signal 9 (killed)")
	logToolExit("codex", 4243, "exit=0 (clean)")

	text := readUseLog(t, dir)
	for _, want := range []string{"pid=4242", "tool=claude", "signal 9", "pid=4243", "tool=codex", "exit=0"} {
		if !strings.Contains(text, want) {
			t.Errorf("use.log missing %q; got:\n%s", want, text)
		}
	}
	if lines := nonEmptyLines(text); lines != 2 {
		t.Errorf("use.log has %d lines, want 2 (appended, not overwritten)", lines)
	}
}

// TestLogToolExitRollKeepsTailAndWritesNewLine is the regression guard for the roll path: crossing the cap must KEEP recent history (a tail, not a wipe) AND land the new line. The old O_TRUNC roll failed both — it left exactly one line — which defeated the whole correlate-a-mass-death purpose.
func TestLogToolExitRollKeepsTailAndWritesNewLine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "everyapi", "use.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	// Pre-fill just over the cap with uniquely-numbered lines so we can tell which survive the roll.
	var b strings.Builder
	for i := 0; b.Len() <= useExitLogMaxBytes; i++ {
		fmt.Fprintf(&b, "2026-01-01T00:00:00.000Z pid=%d tool=filler line-%08d\n", i, i)
	}
	prefill := b.String()
	if err := os.WriteFile(path, []byte(prefill), 0o600); err != nil {
		t.Fatal(err)
	}
	totalLines := nonEmptyLines(prefill)

	logToolExit("claude", 777, "killed by signal 9 (killed)")

	text := readUseLog(t, dir)

	// 1. Rolled below the cap.
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Size() > useExitLogMaxBytes {
		t.Errorf("use.log size %d did not roll below cap %d", info.Size(), useExitLogMaxBytes)
	}
	// 2. The NEW line is present (the #9 gap: a roll that truncated but dropped the write would still pass a size-only check).
	if !strings.Contains(text, "pid=777") || !strings.Contains(text, "signal 9") {
		t.Errorf("rolled use.log lost the new line; got tail:\n%s", lastN(text, 300))
	}
	// 3. It's a TAIL, not a wipe: many prior lines survive, and they are the most recent ones (highest line-NNNN), not the oldest.
	kept := nonEmptyLines(text)
	if kept < 100 {
		t.Errorf("roll kept only %d lines of %d — that's a wipe, not a tail", kept, totalLines)
	}
	if strings.Contains(text, "line-00000000") {
		t.Errorf("roll retained the OLDEST line; expected the newest tail to survive")
	}
	if !strings.Contains(text, fmt.Sprintf("line-%08d", totalLines-1)) {
		t.Errorf("roll dropped the newest filler line; tail is misaligned")
	}
	// 4. No torn partial line at the top (tail is line-aligned).
	first := strings.SplitN(strings.TrimLeft(text, "\n"), "\n", 2)[0]
	if first != "" && !strings.HasPrefix(first, "2026-") {
		t.Errorf("roll left a partial leading line: %q", first)
	}
}

// TestLogToolExitConcurrentWritesNoTear runs many concurrent writers (the mass-death scenario) and asserts every line is intact and countable — no interleaving mid-line, no lost lines. This is what the advisory lock buys; the old code relied on an invalid PIPE_BUF-for-files assumption.
func TestLogToolExitConcurrentWritesNoTear(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	const writers = 50
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			logToolExit("claude", 1000+i, fmt.Sprintf("exit=%d", i))
		}(i)
	}
	wg.Wait()

	text := readUseLog(t, dir)
	if got := nonEmptyLines(text); got != writers {
		t.Errorf("got %d lines, want %d (lost or torn writes under concurrency)", got, writers)
	}
	// Every line must be a complete, well-formed record.
	for _, ln := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if ln == "" {
			continue
		}
		if !strings.HasPrefix(ln, "2026-") || !strings.Contains(ln, "tool=claude") || !strings.Contains(ln, "exit=") {
			t.Errorf("torn/garbled line: %q", ln)
		}
	}
}

// TestLogToolExitBestEffortOnBadConfigDir confirms a write to an unresolvable/unwritable config dir is swallowed and never panics — the diagnostic must not itself fail the launch.
func TestLogToolExitBestEffortOnBadConfigDir(t *testing.T) {
	// Point the config dir at a path whose parent is a regular file, so MkdirAll fails; logToolExit must return cleanly.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", blocker) // ConfigDir joins "everyapi" under this file
	logToolExit("claude", 1, "exit=0 (clean)")
	// No assertion beyond "did not panic / did not hang" — the timeout in logToolExit bounds the latter.
}

func readUseLog(t *testing.T, cfgDir string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(cfgDir, "everyapi", "use.log"))
	if err != nil {
		t.Fatalf("read use.log: %v", err)
	}
	return string(body)
}

func nonEmptyLines(text string) int {
	n := 0
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
