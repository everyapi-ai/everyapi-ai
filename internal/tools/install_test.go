package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRegistry_HasInstallCmds pins that every shipped tool has an
// auto-installable command wired up — losing one silently degrades
// the `everyapi use <tool>` UX back to "print a URL, leave the user
// to install manually." Reviewable change, not silent.
func TestRegistry_HasInstallCmds(t *testing.T) {
	cases := map[string]string{
		"claude": "curl -fsSL https://claude.ai/install.sh | bash",
		"codex":  "npm install -g @openai/codex",
	}
	for name, want := range cases {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		if tool.InstallCmd != want {
			t.Errorf("%s.InstallCmd = %q, want %q", name, tool.InstallCmd, want)
		}
	}
}

// TestCanAutoInstall_ClaudeWindows verifies that claude's Unix-only
// `curl | bash` installer is gated off on Windows. Without this gate
// the install prompt would offer a command we know is going to fail —
// the user is better served by the existing InstallHint message
// pointing at the official Windows setup docs.
func TestCanAutoInstall_ClaudeWindows(t *testing.T) {
	tool, _ := Lookup("claude")
	if !tool.InstallCmdUnixOnly {
		t.Fatal("claude.InstallCmdUnixOnly should be true — curl|bash doesn't run on Windows")
	}
	// CanAutoInstall is platform-aware. We can only assert one side
	// of the branch from the test harness — assert the side that
	// matches the host OS, and assert the gating field for the other.
	if runtime.GOOS == "windows" {
		if CanAutoInstall(tool) {
			t.Error("CanAutoInstall(claude) = true on Windows, want false")
		}
	} else {
		if !CanAutoInstall(tool) {
			t.Error("CanAutoInstall(claude) = false on Unix, want true")
		}
	}
}

// TestCanAutoInstall_NpmCrossPlatform asserts the npm-based installers
// stay available on every platform. npm itself is cross-platform, so
// gating these would needlessly send Windows users back to the
// install-hint URL when `npm install -g @openai/codex` would work.
func TestCanAutoInstall_NpmCrossPlatform(t *testing.T) {
	for _, name := range []string{"codex"} {
		tool, _ := Lookup(name)
		if tool.InstallCmdUnixOnly {
			t.Errorf("%s.InstallCmdUnixOnly = true, but npm is cross-platform", name)
		}
		if !CanAutoInstall(tool) {
			t.Errorf("CanAutoInstall(%s) = false on %s", name, runtime.GOOS)
		}
	}
}

// TestCanAutoInstall_Empty makes sure a tool with no InstallCmd
// (the default zero value) reports false — guards against a future
// tool entry shipping with the field left blank, which would surface
// as "no auto-install available" inside RunInstall instead of being
// caught at the prompt site.
func TestCanAutoInstall_Empty(t *testing.T) {
	tool := &Tool{Name: "blank", ExecName: "blank"}
	if CanAutoInstall(tool) {
		t.Error("CanAutoInstall on a tool with empty InstallCmd should be false")
	}
}

// TestIsInstalled covers both branches with binaries every supported
// platform ships: `sh` exists on Unix and `cmd` on Windows; a name
// chosen to be vanishingly unlikely to land on disk does not.
func TestIsInstalled(t *testing.T) {
	present := &Tool{Name: "_present", ExecName: "sh"}
	if runtime.GOOS == "windows" {
		present.ExecName = "cmd"
	}
	if !IsInstalled(present) {
		t.Errorf("IsInstalled(%s) = false on %s, but it's a system binary", present.ExecName, runtime.GOOS)
	}
	missing := &Tool{Name: "_missing", ExecName: "definitely-not-a-real-binary-zzz"}
	if IsInstalled(missing) {
		t.Errorf("IsInstalled(%s) = true, want false", missing.ExecName)
	}
}

// TestInstallPromptDefault pins the Y/N default per installer flavor:
// curl|bash (InstallCmdUnixOnly) defaults to No so a single press of
// Enter never runs a remote shell script; routine npm installs
// default to Yes so the common case stays one keystroke.
func TestInstallPromptDefault(t *testing.T) {
	cases := map[string]bool{
		"claude": false, // curl|bash → default No
		"codex":  true,  // npm → default Yes
		"gemini": true,  // npm → default Yes
	}
	for name, want := range cases {
		tool, _ := Lookup(name)
		if got := tool.InstallPromptDefault(); got != want {
			t.Errorf("%s.InstallPromptDefault() = %v, want %v", name, got, want)
		}
	}
}

// TestInstallerMissing covers the pre-install PATH probe that turns a
// doomed `sh -c "npm install …"` (cryptic "npm: command not found") into
// an actionable message. The probe targets the InstallCmd's leading word.
func TestInstallerMissing(t *testing.T) {
	// No InstallCmd → nothing to gate.
	if got := InstallerMissing(&Tool{Name: "blank", ExecName: "blank"}); got != "" {
		t.Errorf("InstallerMissing(no InstallCmd) = %q, want \"\"", got)
	}
	// Installer command present on PATH → "" (sh exists on Unix, cmd on
	// Windows; both are guaranteed system binaries).
	present := "sh"
	if runtime.GOOS == "windows" {
		present = "cmd"
	}
	withPresent := &Tool{Name: "present", ExecName: "x", InstallCmd: present + " -c true"}
	if got := InstallerMissing(withPresent); got != "" {
		t.Errorf("InstallerMissing(present installer %q) = %q, want \"\"", present, got)
	}
	// Installer command absent from PATH → its name is reported.
	missingCmd := "definitely-not-a-real-pkg-manager-zzz"
	withMissing := &Tool{Name: "missing", ExecName: "x", InstallCmd: missingCmd + " install -g foo"}
	if got := InstallerMissing(withMissing); got != missingCmd {
		t.Errorf("InstallerMissing(missing installer) = %q, want %q", got, missingCmd)
	}
}

// TestRunInstall_RejectsWhenNoCmd guards the contract that callers
// must gate with CanAutoInstall — but the inner check exists so a
// future caller that forgets the gate gets a plain error instead of
// shelling out an empty command.
func TestRunInstall_RejectsWhenNoCmd(t *testing.T) {
	tool := &Tool{Name: "noinstall", ExecName: "noinstall"}
	err := RunInstall(tool)
	if err == nil {
		t.Fatal("RunInstall with empty InstallCmd should error")
	}
}

// TestRunInstall_CommandFailure asserts a non-zero exit from the
// install command surfaces as a wrapped error (not nil, not a
// misclassified ErrInstalledButNotOnPath). Uses `false` on Unix and
// `cmd /C exit 1` semantics on Windows via the shell switch in
// RunInstall.
func TestRunInstall_CommandFailure(t *testing.T) {
	tool := &Tool{
		Name:       "fail",
		ExecName:   "definitely-not-real-zzz",
		InstallCmd: "false",
	}
	if runtime.GOOS == "windows" {
		tool.InstallCmd = "exit 1"
	}
	err := RunInstall(tool)
	if err == nil {
		t.Fatal("RunInstall on a failing command should error")
	}
	var notOnPath *ErrInstalledButNotOnPath
	if errors.As(err, &notOnPath) {
		t.Errorf("command-failure error misclassified as ErrInstalledButNotOnPath: %v", err)
	}
}

// TestRunInstall_PipelineFirstStageFailure pins the pipefail fix: for a
// `curl … | bash`-style installer, a failure in the FIRST stage (curl)
// must surface as an error rather than being masked by the pipeline's
// aggregate exit code (bash's 0). Without pipefail this returned
// ErrInstalledButNotOnPath — a wrong "installed but not on PATH"
// diagnosis of a failed download. Requires bash (present wherever these
// installers could run at all); skipped otherwise.
func TestRunInstall_PipelineFirstStageFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only: pipeline + pipefail semantics")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available; pipefail path not exercised")
	}
	tool := &Tool{
		Name:       "pipefail",
		ExecName:   "definitely-not-real-pipefail-zzz",
		InstallCmd: "false | true", // first stage fails, last stage exits 0
	}
	err := RunInstall(tool)
	if err == nil {
		t.Fatal("RunInstall should error when the first pipeline stage fails")
	}
	var notOnPath *ErrInstalledButNotOnPath
	if errors.As(err, &notOnPath) {
		t.Errorf("failed pipeline misclassified as ErrInstalledButNotOnPath: %v", err)
	}
}

// TestRunInstall_InstalledButNotOnPath covers the post-install
// LookPath re-check: when the install command exits 0 but the binary
// still isn't findable (the classic `npm install -g` with npm's
// global bin missing from PATH), the caller gets a typed
// ErrInstalledButNotOnPath so cmd/use can render the localized "open
// a new shell" message. `true` exits 0 on Unix; `cmd /C exit 0`
// likewise on Windows.
func TestRunInstall_InstalledButNotOnPath(t *testing.T) {
	tool := &Tool{
		Name:       "missing-after-install",
		ExecName:   "definitely-not-real-after-install-zzz",
		InstallCmd: "true",
	}
	if runtime.GOOS == "windows" {
		tool.InstallCmd = "exit 0"
	}
	err := RunInstall(tool)
	if err == nil {
		t.Fatal("RunInstall should error when ExecName isn't on PATH after install")
	}
	var notOnPath *ErrInstalledButNotOnPath
	if !errors.As(err, &notOnPath) {
		t.Fatalf("got %T (%v), want *ErrInstalledButNotOnPath", err, err)
	}
	if notOnPath.Tool != tool {
		t.Errorf("ErrInstalledButNotOnPath.Tool = %v, want the input tool", notOnPath.Tool)
	}
}

// TestRunInstall_HappyPath verifies the end-to-end success branch:
// an install command that drops a binary into a tmp dir we then add
// to PATH succeeds, RunInstall finds the binary via post-install
// LookPath, and returns nil. Unix-only because the trivial executable
// creation (`touch + chmod +x`) doesn't translate to cmd.exe.
func TestRunInstall_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only: relies on touch/chmod semantics for the fake installer")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-tool")
	// Prepend the tmp dir to PATH so post-install LookPath finds the
	// binary the installer "creates" below.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tool := &Tool{
		Name:       "fake",
		ExecName:   "fake-tool",
		InstallCmd: "touch " + bin + " && chmod +x " + bin,
	}
	if err := RunInstall(tool); err != nil {
		t.Fatalf("happy-path RunInstall returned %v", err)
	}
	if !IsInstalled(tool) {
		t.Error("after happy-path RunInstall, IsInstalled should be true")
	}
}

// TestErrInstalledButNotOnPath_ErrorMessage pins that the typed
// error's English fallback still carries the ExecName — the cmd/use
// layer renders a localized message, but library-level errors still
// need to be debuggable when surfaced raw (logs, %v in stack traces).
func TestErrInstalledButNotOnPath_ErrorMessage(t *testing.T) {
	err := &ErrInstalledButNotOnPath{Tool: &Tool{ExecName: "widget"}}
	if msg := err.Error(); msg == "" {
		t.Fatal("Error() should not be empty")
	}
	if !strings.Contains(err.Error(), "widget") {
		t.Errorf("Error() %q should mention the ExecName", err.Error())
	}
}
