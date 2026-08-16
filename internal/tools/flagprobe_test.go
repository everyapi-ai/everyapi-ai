//go:build !windows

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const probeFlag = "--dangerously-bypass-hook-trust"

// writeProbeScript installs an executable shell script under a fresh dir and puts that dir first on $PATH, so ResolveExec finds it as `name`.
func writeProbeScript(t *testing.T, name, body string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write probe script: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// wrapperScript models the real hazard: a shim on $PATH that PREPENDS the flag itself before the tool parses argv (cmux does this for codex), plus a parser that rejects a repeated flag the way codex's clap does.
const wrapperScript = `#!/bin/sh
# The wrapper always injects its own copy, exactly like cmux-codex-wrapper.
set -- ` + probeFlag + ` "$@"
seen=0
for a in "$@"; do
  [ "$a" = "` + probeFlag + `" ] && seen=$((seen+1))
done
if [ "$seen" -gt 1 ]; then
  echo "error: the argument '` + probeFlag + `' cannot be used multiple times" >&2
  exit 2
fi
exit 0
`

// plainScript is a tool with no wrapper: it accepts the flag once and would accept EveryAPI's copy fine.
const plainScript = `#!/bin/sh
exit 0
`

// TestFlagProbeRejectsFlagAWrapperAlreadyInjects is the regression guard for the launch-aborting duplicate. The flag never appears in the args EveryAPI knows about, so containsFlag cannot see it; only executing the binary can.
func TestFlagProbeRejectsFlagAWrapperAlreadyInjects(t *testing.T) {
	writeProbeScript(t, "faketool", wrapperScript)
	tool := &Tool{Name: "faketool", ExecName: "faketool", FlagProbeArgs: []string{"exec", "--help"}}

	if NewFlagProbe(tool).Accepts(probeFlag) {
		t.Fatalf("Accepts(%s) = true for a wrapper that already injects it; "+
			"the launch would die with 'cannot be used multiple times'", probeFlag)
	}
}

// TestFlagProbeAcceptsFlagOnUnwrappedTool guards the other direction: the probe must not become a blanket "never add the flag". A normal install has no wrapper and must still get the flag the user opted into.
func TestFlagProbeAcceptsFlagOnUnwrappedTool(t *testing.T) {
	writeProbeScript(t, "faketool", plainScript)
	tool := &Tool{Name: "faketool", ExecName: "faketool", FlagProbeArgs: []string{"exec", "--help"}}

	if !NewFlagProbe(tool).Accepts(probeFlag) {
		t.Fatalf("Accepts(%s) = false for an unwrapped tool; the user's preference was dropped", probeFlag)
	}
}

// TestFlagProbeAcceptsWhenBaselineAlsoFails pins the attribution rule. A tool that fails its own probe argv (old version, no `exec` subcommand) proves nothing about the flag, so the preference must survive.
func TestFlagProbeAcceptsWhenBaselineAlsoFails(t *testing.T) {
	writeProbeScript(t, "faketool", "#!/bin/sh\nexit 1\n")
	tool := &Tool{Name: "faketool", ExecName: "faketool", FlagProbeArgs: []string{"exec", "--help"}}

	if !NewFlagProbe(tool).Accepts(probeFlag) {
		t.Fatal("Accepts = false when the flag-free baseline fails too; " +
			"an inconclusive probe must not drop the flag")
	}
}

// TestFlagProbeAcceptsWithoutProbeArgs covers every tool that has not opted in: no FlagProbeArgs means no probing and no behavior change.
func TestFlagProbeAcceptsWithoutProbeArgs(t *testing.T) {
	writeProbeScript(t, "faketool", wrapperScript)
	tool := &Tool{Name: "faketool", ExecName: "faketool"}

	if !NewFlagProbe(tool).Accepts(probeFlag) {
		t.Fatal("Accepts = false for a tool that declares no FlagProbeArgs")
	}
}

// TestFlagProbeAcceptsWhenExecutableMissing keeps a resolution failure from silently rewriting argv. Exec reports the missing tool; the probe stays out of it.
func TestFlagProbeAcceptsWhenExecutableMissing(t *testing.T) {
	tool := &Tool{
		Name:          "everyapi-absent-tool-zzz",
		ExecName:      "everyapi-absent-tool-zzz",
		FlagProbeArgs: []string{"exec", "--help"},
	}
	if !NewFlagProbe(tool).Accepts(probeFlag) {
		t.Fatal("Accepts = false when the executable does not resolve")
	}
}

// TestFlagProbeRunsBaselineOnce guards the cost: the control run is shared across flags, so probing both of codex's non-repeatable flags costs three launches, not four.
func TestFlagProbeRunsBaselineOnce(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	writeProbeScript(t, "faketool", "#!/bin/sh\necho x >> "+counter+"\n"+wrapperScript[len("#!/bin/sh\n"):])
	tool := &Tool{Name: "faketool", ExecName: "faketool", FlagProbeArgs: []string{"exec", "--help"}}

	probe := NewFlagProbe(tool)
	probe.Accepts(probeFlag)
	probe.Accepts(probeFlag)

	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read run counter: %v", err)
	}
	runs := strings.Count(string(data), "\n")
	if want := 3; runs != want {
		t.Fatalf("probe launched %d times, want %d (two flagged runs + one shared baseline)", runs, want)
	}
}

// TestCodexDeclaresFlagProbeArgs pins the registry wiring: codex is the tool whose parser rejects repeated bypass flags, so it must carry probe args.
func TestCodexDeclaresFlagProbeArgs(t *testing.T) {
	codex, err := Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(codex.FlagProbeArgs) == 0 {
		t.Fatal("codex declares no FlagProbeArgs; duplicate-flag launches go undetected")
	}
}
