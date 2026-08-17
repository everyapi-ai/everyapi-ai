package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var desktopCLITools = []string{
	"claude", "codex", "opencode", "aider", "goose", "crush", "cline",
	"openclaw", "continue", "kilo", "pi", "vibe", "copilot", "droid",
	"openhands", "forge", "llxprt", "grok", "qwen-code", "kimi-code",
	"hermes", "librefang", "open-webui", "deepseek-harness",
}

// TestEveryDesktopCLIHasAReviewedInstallerOnEveryPlatform is the contract for the Connect catalogue: a visible CLI target must never pretend that opening documentation installed it. Gemini and Antigravity are intentionally absent because they are not desktop catalogue targets.
func TestEveryDesktopCLIHasAReviewedInstallerOnEveryPlatform(t *testing.T) {
	for _, name := range desktopCLITools {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		for _, goos := range []string{"darwin", "linux", "windows"} {
			if installCommandForOS(tool, goos).empty() {
				t.Errorf("%s has no reviewed %s installer; Connect would open a webpage", name, goos)
			}
		}
	}
}

func TestNewDesktopInstallersUseOfficialCommandsAndDeterministicOutputs(t *testing.T) {
	cases := map[string]struct {
		unix        string
		windows     []string
		extraBinDir string
	}{
		"goose": {
			unix:        "curl -fsSL https://github.com/aaif-goose/goose/releases/download/stable/download_cli.sh | CONFIGURE=false bash || curl -fsSL https://dl.everyapi.ai/cli-mirrors/install.sh | bash -s -- goose",
			windows:     []string{"powershell", "-ExecutionPolicy", "ByPass", "-Command", "& ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/install.ps1))) -Tool goose"},
			extraBinDir: ".local/bin",
		},
		"vibe": {
			unix:        "curl -LsSf https://mistral.ai/vibe/install.sh | bash || (curl -fsSL https://dl.everyapi.ai/cli-mirrors/uv/install.sh | sh && UV_PYTHON_INSTALL_MIRROR=https://dl.everyapi.ai/cli-mirrors/python UV_DEFAULT_INDEX=https://mirrors.aliyun.com/pypi/simple/ \"$HOME/.local/bin/uv\" tool install --python 3.12 mistral-vibe)",
			windows:     []string{"powershell", "-ExecutionPolicy", "ByPass", "-Command", "irm https://dl.everyapi.ai/cli-mirrors/uv/install.ps1 | iex; $env:UV_PYTHON_INSTALL_MIRROR='https://dl.everyapi.ai/cli-mirrors/python'; $env:UV_DEFAULT_INDEX='https://mirrors.aliyun.com/pypi/simple/'; & \"$env:USERPROFILE\\.local\\bin\\uv.exe\" tool install --python 3.12 mistral-vibe"},
			extraBinDir: ".local/bin",
		},
		"librefang": {
			unix:        "curl -fsSL https://librefang.ai/install.sh | LIBREFANG_AUTO_START=0 sh || curl -fsSL https://dl.everyapi.ai/cli-mirrors/install.sh | bash -s -- librefang",
			windows:     []string{"powershell", "-ExecutionPolicy", "ByPass", "-Command", "& ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/install.ps1))) -Tool librefang"},
			extraBinDir: ".librefang/bin",
		},
		"open-webui": {
			unix:        "(curl -LsSf https://astral.sh/uv/install.sh | sh && \"$HOME/.local/bin/uv\" tool install --python 3.11 open-webui) || (curl -fsSL https://dl.everyapi.ai/cli-mirrors/uv/install.sh | sh && UV_PYTHON_INSTALL_MIRROR=https://dl.everyapi.ai/cli-mirrors/python UV_DEFAULT_INDEX=https://mirrors.aliyun.com/pypi/simple/ \"$HOME/.local/bin/uv\" tool install --python 3.11 open-webui)",
			windows:     []string{"powershell", "-ExecutionPolicy", "ByPass", "-Command", "irm https://dl.everyapi.ai/cli-mirrors/uv/install.ps1 | iex; $env:UV_PYTHON_INSTALL_MIRROR='https://dl.everyapi.ai/cli-mirrors/python'; $env:UV_DEFAULT_INDEX='https://mirrors.aliyun.com/pypi/simple/'; & \"$env:USERPROFILE\\.local\\bin\\uv.exe\" tool install --python 3.11 open-webui"},
			extraBinDir: ".local/bin",
		},
	}
	for name, want := range cases {
		tool, _ := Lookup(name)
		if tool.InstallCmd != want.unix {
			t.Errorf("%s.InstallCmd = %q, want %q", name, tool.InstallCmd, want.unix)
		}
		if !reflect.DeepEqual(tool.InstallCmdWindows, want.windows) {
			t.Errorf("%s.InstallCmdWindows = %q, want %q", name, tool.InstallCmdWindows, want.windows)
		}
		if !containsString(tool.ExtraBinDirs, want.extraBinDir) {
			t.Errorf("%s.ExtraBinDirs = %v, missing %q", name, tool.ExtraBinDirs, want.extraBinDir)
		}
		if tool.InstallPromptDefault() {
			t.Errorf("%s remote installer must default to No", name)
		}
	}
}

func TestExistingUnixInstallersHaveReviewedWindowsCommands(t *testing.T) {
	cases := map[string][]string{
		"claude": {
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"irm https://dl.everyapi.ai/claude-code/install.ps1 | iex",
		},
		"openhands": {
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"irm https://dl.everyapi.ai/cli-mirrors/uv/install.ps1 | iex; $env:UV_PYTHON_INSTALL_MIRROR='https://dl.everyapi.ai/cli-mirrors/python'; $env:UV_DEFAULT_INDEX='https://mirrors.aliyun.com/pypi/simple/'; & \"$env:USERPROFILE\\.local\\bin\\uv.exe\" tool install openhands --python 3.12",
		},
		"forge": {
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"& ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/install.ps1))) -Tool forge",
		},
		"hermes": {
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"& ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/hermes/install.ps1))) -NonInteractive",
		},
	}
	for name, want := range cases {
		tool, _ := Lookup(name)
		if !reflect.DeepEqual(tool.InstallCmdWindows, want) {
			t.Errorf("%s.InstallCmdWindows = %q, want %q", name, tool.InstallCmdWindows, want)
		}
	}
}

func TestClaudeInstallerUsesTheChinaMirrorFallback(t *testing.T) {
	tool, err := Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	wantUnix := "curl -fsSL https://claude.ai/install.sh | bash || curl -fsSL https://dl.everyapi.ai/claude-code/install.sh | bash"
	if tool.InstallCmd != wantUnix {
		t.Errorf("claude InstallCmd = %q, want %q", tool.InstallCmd, wantUnix)
	}
	wantWindows := []string{
		"powershell", "-ExecutionPolicy", "ByPass", "-Command",
		"irm https://dl.everyapi.ai/claude-code/install.ps1 | iex",
	}
	if !reflect.DeepEqual(tool.InstallCmdWindows, wantWindows) {
		t.Errorf("claude InstallCmdWindows = %q, want %q", tool.InstallCmdWindows, wantWindows)
	}
}

func TestNpmInstallersRetryThroughTheChinaRegistry(t *testing.T) {
	npmTools := []string{
		"codex", "opencode", "gemini", "crush", "cline", "openclaw",
		"continue", "deepseek-harness", "kilo", "pi", "copilot", "droid",
		"llxprt", "grok", "qwen-code", "kimi-code",
	}
	for _, name := range npmTools {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(tool.InstallCmd, " || npm install ") {
			t.Errorf("%s installer does not retry npm: %q", name, tool.InstallCmd)
		}
		tencent := "--registry=https://mirrors.cloud.tencent.com/npm/"
		npmMirror := "--registry=https://registry.npmmirror.com"
		if !strings.Contains(tool.InstallCmd, tencent) || !strings.Contains(tool.InstallCmd, npmMirror) {
			t.Errorf("%s installer does not contain both China registry fallbacks: %q", name, tool.InstallCmd)
		}
		fallbackAt := strings.Index(tool.InstallCmd, " || ")
		tencentAt := strings.Index(tool.InstallCmd, tencent)
		npmMirrorAt := strings.Index(tool.InstallCmd, npmMirror)
		if fallbackAt < 0 || tencentAt < fallbackAt || npmMirrorAt < tencentAt {
			t.Errorf("%s must try official npm, Tencent, then npmmirror: %q", name, tool.InstallCmd)
		}
	}
}

func TestEveryRegisteredAutoInstallerHasAChinaReachablePath(t *testing.T) {
	for name, tool := range Registry {
		if strings.TrimSpace(tool.InstallCmd) == "" {
			continue
		}
		if strings.Contains(tool.InstallCmd, "npm install") {
			if !strings.Contains(tool.InstallCmd, " || npm install ") ||
				!strings.Contains(tool.InstallCmd, "--registry=https://mirrors.cloud.tencent.com/npm/") ||
				!strings.Contains(tool.InstallCmd, "--registry=https://registry.npmmirror.com") {
				t.Errorf("%s npm installer has no failure-only China registry path: %q", name, tool.InstallCmd)
			}
		} else if !strings.Contains(tool.InstallCmd, "https://dl.everyapi.ai/") {
			t.Errorf("%s remote installer has no EveryAPI download fallback: %q", name, tool.InstallCmd)
		}
		if len(tool.InstallCmdWindows) > 0 {
			windows := strings.Join(tool.InstallCmdWindows, " ")
			if !strings.Contains(windows, "https://dl.everyapi.ai/") && !strings.Contains(windows, "npm install") {
				t.Errorf("%s Windows installer has no China-reachable path: %q", name, tool.InstallCmdWindows)
			}
		}
	}
}

func TestDirectDownloadInstallersRetryThroughEveryAPIMirror(t *testing.T) {
	for _, name := range []string{"antigravity", "goose", "crush", "openhands", "forge", "librefang"} {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(tool.InstallCmd, "https://dl.everyapi.ai/cli-mirrors/install.sh") {
			t.Errorf("%s Unix installer has no OSS binary fallback: %q", name, tool.InstallCmd)
		}
		if name == "openhands" {
			continue // OpenHands publishes no Windows binary; its Windows fallback is the mirrored uv/PyPI path below.
		}
		if got := strings.Join(tool.InstallCmdWindows, " "); !strings.Contains(got, "https://dl.everyapi.ai/cli-mirrors/install.ps1") {
			t.Errorf("%s Windows installer has no OSS binary fallback: %q", name, tool.InstallCmdWindows)
		}
	}
}

func TestPythonToolFallbacksUseMirroredUvAndAliyunPyPI(t *testing.T) {
	for name, pythonVersion := range map[string]string{"aider": "3.12", "vibe": "3.12", "open-webui": "3.11"} {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(tool.InstallCmd, "https://dl.everyapi.ai/cli-mirrors/uv/install.sh") {
			t.Errorf("%s Unix installer has no mirrored uv fallback: %q", name, tool.InstallCmd)
		}
		if !strings.Contains(tool.InstallCmd, "https://mirrors.aliyun.com/pypi/simple/") {
			t.Errorf("%s Unix installer has no Aliyun PyPI fallback: %q", name, tool.InstallCmd)
		}
		if !strings.Contains(tool.InstallCmd, "UV_PYTHON_INSTALL_MIRROR=https://dl.everyapi.ai/cli-mirrors/python") {
			t.Errorf("%s Unix installer has no managed-Python mirror: %q", name, tool.InstallCmd)
		}
		if !strings.Contains(tool.InstallCmd, "--python "+pythonVersion) {
			t.Errorf("%s Unix fallback may select an unmirrored Python version: %q", name, tool.InstallCmd)
		}
		if got := strings.Join(tool.InstallCmdWindows, " "); !strings.Contains(got, "https://dl.everyapi.ai/cli-mirrors/uv/install.ps1") || !strings.Contains(got, "https://mirrors.aliyun.com/pypi/simple/") {
			t.Errorf("%s Windows installer lacks the mirrored uv/PyPI fallback: %q", name, tool.InstallCmdWindows)
		}
		if got := strings.Join(tool.InstallCmdWindows, " "); !strings.Contains(got, "UV_PYTHON_INSTALL_MIRROR") {
			t.Errorf("%s Windows installer has no managed-Python mirror: %q", name, tool.InstallCmdWindows)
		}
		if got := strings.Join(tool.InstallCmdWindows, " "); !strings.Contains(got, "--python "+pythonVersion) {
			t.Errorf("%s Windows fallback may select an unmirrored Python version: %q", name, tool.InstallCmdWindows)
		}
	}
	openhands, _ := Lookup("openhands")
	if got := strings.Join(openhands.InstallCmdWindows, " "); !strings.Contains(got, "https://dl.everyapi.ai/cli-mirrors/uv/install.ps1") || !strings.Contains(got, "https://mirrors.aliyun.com/pypi/simple/") {
		t.Errorf("openhands Windows installer lacks the mirrored uv/PyPI fallback: %q", openhands.InstallCmdWindows)
	}
	if got := strings.Join(openhands.InstallCmdWindows, " "); !strings.Contains(got, "UV_PYTHON_INSTALL_MIRROR") {
		t.Errorf("openhands Windows installer has no managed-Python mirror: %q", openhands.InstallCmdWindows)
	}
	if got := strings.Join(openhands.InstallCmdWindows, " "); !strings.Contains(got, "--python 3.12") {
		t.Errorf("openhands Windows fallback may select an unmirrored Python version: %q", openhands.InstallCmdWindows)
	}
}

func TestHermesInstallerSkipsPersistentSetup(t *testing.T) {
	tool, _ := Lookup("hermes")
	if !strings.Contains(tool.InstallCmd, "bash -s -- --non-interactive --skip-setup") {
		t.Fatalf("Hermes Unix installer is interactive: %q", tool.InstallCmd)
	}
	if got := strings.Join(tool.InstallCmdWindows, " "); !strings.Contains(got, "-NonInteractive") {
		t.Fatalf("Hermes Windows installer is interactive: %q", tool.InstallCmdWindows)
	}
	if !strings.Contains(tool.InstallCmd, "https://dl.everyapi.ai/cli-mirrors/hermes/install.sh") {
		t.Fatalf("Hermes Unix installer has no mirrored repository fallback: %q", tool.InstallCmd)
	}
	if got := strings.Join(tool.InstallCmdWindows, " "); !strings.Contains(got, "https://dl.everyapi.ai/cli-mirrors/hermes/install.ps1") {
		t.Fatalf("Hermes Windows installer has no mirrored repository fallback: %q", tool.InstallCmdWindows)
	}
}

func TestAiderAutoInstall(t *testing.T) {
	tool, err := Lookup("aider")
	if err != nil {
		t.Fatalf("Lookup(aider): %v", err)
	}
	if !CanAutoInstall(tool) {
		t.Fatalf("CanAutoInstall(aider) = false on %s, want true", runtime.GOOS)
	}
	wantWindows := []string{
		"powershell", "-ExecutionPolicy", "ByPass", "-Command",
		"irm https://dl.everyapi.ai/cli-mirrors/uv/install.ps1 | iex; $env:UV_PYTHON_INSTALL_MIRROR='https://dl.everyapi.ai/cli-mirrors/python'; $env:UV_DEFAULT_INDEX='https://mirrors.aliyun.com/pypi/simple/'; & \"$env:USERPROFILE\\.local\\bin\\uv.exe\" tool install --python 3.12 aider-chat",
	}
	if !reflect.DeepEqual(tool.InstallCmdWindows, wantWindows) {
		t.Errorf("InstallCmdWindows = %q", tool.InstallCmdWindows)
	}
	if tool.InstallPromptDefault() {
		t.Error("Aider's remote installer must default to No")
	}
	if !containsString(tool.ExtraBinDirs, ".local/bin") {
		t.Errorf("ExtraBinDirs = %v, missing .local/bin", tool.ExtraBinDirs)
	}
}

func TestInstallCommandSelectsThePlatformInstaller(t *testing.T) {
	tool := &Tool{
		InstallCmd:         "unix-installer",
		InstallCmdUnixOnly: true,
		InstallCmdWindows:  []string{"windows-installer", "--flag", "value with spaces"},
	}
	if got := installCommandForOS(tool, "darwin"); got.shell != "unix-installer" || got.executable != "" {
		t.Errorf("darwin command = %#v", got)
	}
	if got := installCommandForOS(tool, "linux"); got.shell != "unix-installer" || got.executable != "" {
		t.Errorf("linux command = %#v", got)
	}
	windows := installCommandForOS(tool, "windows")
	if windows.executable != "windows-installer" || !reflect.DeepEqual(windows.args, []string{"--flag", "value with spaces"}) || windows.shell != "" {
		t.Errorf("windows command = %#v", windows)
	}
	if got := windows.display(); got != `windows-installer --flag "value with spaces"` {
		t.Errorf("windows display = %q", got)
	}
	tool.InstallCmdWindows = nil
	if got := installCommandForOS(tool, "windows"); !got.empty() {
		t.Errorf("Windows command without an override = %#v, want empty", got)
	}
	tool.InstallCmdUnixOnly = false
	if got := installCommandForOS(tool, "windows"); got.shell != "unix-installer" {
		t.Errorf("cross-platform Windows command = %#v", got)
	}
}

func TestBuildInstallCommandKeepsWindowsArgvStructured(t *testing.T) {
	tool, _ := Lookup("aider")
	command := buildInstallCommand(installCommandForOS(tool, "windows"), "windows")
	want := []string{
		"powershell", "-ExecutionPolicy", "ByPass", "-Command",
		"irm https://dl.everyapi.ai/cli-mirrors/uv/install.ps1 | iex; $env:UV_PYTHON_INSTALL_MIRROR='https://dl.everyapi.ai/cli-mirrors/python'; $env:UV_DEFAULT_INDEX='https://mirrors.aliyun.com/pypi/simple/'; & \"$env:USERPROFILE\\.local\\bin\\uv.exe\" tool install --python 3.12 aider-chat",
	}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("Windows installer argv = %q, want %q", command.Args, want)
	}
	if command.Args[0] == "cmd" {
		t.Fatal("PowerShell installer must not be reparsed through cmd.exe")
	}
}

func TestRegistry_RemoteInstallScriptsAreImmutable(t *testing.T) {
	for name, tool := range Registry {
		cmd := tool.InstallCmd
		if !strings.Contains(cmd, "raw.githubusercontent.com/") {
			continue
		}
		for _, mutableRef := range []string{"/main/", "/master/", "/HEAD/"} {
			if strings.Contains(cmd, mutableRef) {
				t.Errorf("%s installer executes mutable Git ref %q: %s", name, mutableRef, cmd)
			}
		}
	}
}

// TestCanAutoInstall_ClaudeCrossPlatform verifies that Claude selects its shell
// installer on Unix and its structured PowerShell installer on Windows.
func TestCanAutoInstall_ClaudeCrossPlatform(t *testing.T) {
	tool, _ := Lookup("claude")
	if !tool.InstallCmdUnixOnly {
		t.Fatal("claude.InstallCmdUnixOnly should be true — curl|bash doesn't run on Windows")
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if command := installCommandForOS(tool, goos); command.empty() {
			t.Errorf("installCommandForOS(claude, %q) is empty, want an installer", goos)
		}
	}
	if !CanAutoInstall(tool) {
		t.Errorf("CanAutoInstall(claude) = false on %s, want true", runtime.GOOS)
	}
}

// TestCanAutoInstall_NpmCrossPlatform asserts the npm-based installers stay available on every platform. npm itself is cross-platform, so gating these would needlessly send Windows users back to the install-hint URL when `npm install -g @openai/codex` would work.
func TestCanAutoInstall_NpmCrossPlatform(t *testing.T) {
	for _, name := range []string{"codex", "gemini", "openclaw", "continue", "kilo", "pi", "copilot", "droid"} {
		tool, _ := Lookup(name)
		if tool.InstallCmdUnixOnly {
			t.Errorf("%s.InstallCmdUnixOnly = true, but npm is cross-platform", name)
		}
		if !CanAutoInstall(tool) {
			t.Errorf("CanAutoInstall(%s) = false on %s", name, runtime.GOOS)
		}
	}
}

// TestCanAutoInstall_Empty makes sure a tool with no InstallCmd (the default zero value) reports false — guards against a future tool entry shipping with the field left blank, which would surface as "no auto-install available" inside RunInstall instead of being caught at the prompt site.
func TestCanAutoInstall_Empty(t *testing.T) {
	tool := &Tool{Name: "blank", ExecName: "blank"}
	if CanAutoInstall(tool) {
		t.Error("CanAutoInstall on a tool with empty InstallCmd should be false")
	}
}

// TestIsInstalled covers both branches with binaries every supported platform ships: `sh` exists on Unix and `cmd` on Windows; a name chosen to be vanishingly unlikely to land on disk does not.
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

// TestInstallPromptDefault pins the Y/N default per installer flavor: curl|bash (InstallCmdUnixOnly) defaults to No so a single press of Enter never runs a remote shell script; routine npm installs default to Yes so the common case stays one keystroke.
func TestInstallPromptDefault(t *testing.T) {
	cases := map[string]bool{
		"claude":      false, // curl|bash → default No
		"antigravity": false, // curl|bash → default No
		"gemini":      true,  // npm → default Yes
		"codex":       true,  // npm → default Yes
		"grok":        true,  // npm → default Yes
		"openclaw":    true,  // npm → default Yes
		"copilot":     true,  // npm → default Yes
		"droid":       true,  // npm → default Yes
	}
	for name, want := range cases {
		tool, _ := Lookup(name)
		if got := tool.InstallPromptDefault(); got != want {
			t.Errorf("%s.InstallPromptDefault() = %v, want %v", name, got, want)
		}
	}
}

// TestInstallerMissing covers the pre-install PATH probe that turns a doomed `sh -c "npm install …"` (cryptic "npm: command not found") into an actionable message. The probe targets the InstallCmd's leading word.
func TestInstallerMissing(t *testing.T) {
	// No InstallCmd → nothing to gate.
	if got := InstallerMissing(&Tool{Name: "blank", ExecName: "blank"}); got != "" {
		t.Errorf("InstallerMissing(no InstallCmd) = %q, want \"\"", got)
	}
	// Installer command present on PATH → "" (sh exists on Unix, cmd on Windows; both are guaranteed system binaries).
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

// TestRunInstall_RejectsWhenNoCmd guards the contract that callers must gate with CanAutoInstall — but the inner check exists so a future caller that forgets the gate gets a plain error instead of shelling out an empty command.
func TestRunInstall_RejectsWhenNoCmd(t *testing.T) {
	tool := &Tool{Name: "noinstall", ExecName: "noinstall"}
	err := RunInstall(tool)
	if err == nil {
		t.Fatal("RunInstall with empty InstallCmd should error")
	}
}

// TestRunInstall_CommandFailure asserts a non-zero exit from the install command surfaces as a wrapped error (not nil, not a misclassified ErrInstalledButNotOnPath). Uses `false` on Unix and `cmd /C exit 1` semantics on Windows via the shell switch in RunInstall.
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

// TestRunInstall_PipelineFirstStageFailure pins the pipefail fix: for a `curl … | bash`-style installer, a failure in the FIRST stage (curl) must surface as an error rather than being masked by the pipeline's aggregate exit code (bash's 0). Without pipefail this returned ErrInstalledButNotOnPath — a wrong "installed but not on PATH" diagnosis of a failed download. Requires bash (present wherever these installers could run at all); skipped otherwise.
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

// TestRunInstall_InstalledButNotOnPath covers the post-install LookPath re-check: when the install command exits 0 but the binary still isn't findable (the classic `npm install -g` with npm's global bin missing from PATH), the caller gets a typed ErrInstalledButNotOnPath so cmd/use can render the localized "open a new shell" message. `true` exits 0 on Unix; `cmd /C exit 0` likewise on Windows.
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

// TestErrInstalledButNotOnPath_MessageIsNotNpmSpecific pins that the "searched" list and its prose stay accurate for non-npm tools. The searched set now includes a tool's ExtraBinDirs, so a message naming npm's global bin would be telling a gemini user to fix a directory that was never involved.
func TestErrInstalledButNotOnPath_MessageIsNotNpmSpecific(t *testing.T) {
	err := &ErrInstalledButNotOnPath{
		Tool: &Tool{Name: "geminiish", ExecName: "agy", ExtraBinDirs: []string{".local/bin"}},
		Dirs: []string{"/home/u/.local/bin"},
	}
	msg := err.Error()
	if strings.Contains(msg, "npm") {
		t.Errorf("message names npm for a non-npm tool: %q", msg)
	}
	if !strings.Contains(msg, "/home/u/.local/bin") {
		t.Errorf("message omits the searched dir: %q", msg)
	}
}

// TestRunInstall_ReportsExtraBinDirs pins that a failed post-install re-check names the tool's own install directory. Without this the user gets "not on PATH" with no concrete dir to add, which is the dead end that made `use gemini` unrecoverable.
func TestRunInstall_ReportsExtraBinDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", t.TempDir())
	}
	tool := &Tool{
		Name:         "extradirs",
		ExecName:     "definitely-not-real-extradirs-zzz",
		InstallCmd:   "true",
		ExtraBinDirs: []string{".local/bin"},
	}
	if runtime.GOOS == "windows" {
		tool.InstallCmd = "exit 0"
	}
	var notOnPath *ErrInstalledButNotOnPath
	if err := RunInstall(tool); !errors.As(err, &notOnPath) {
		t.Fatalf("got %T (%v), want *ErrInstalledButNotOnPath", err, err)
	}
	if len(notOnPath.Dirs) == 0 {
		t.Fatal("Dirs is empty; the tool's ExtraBinDirs should be reported as searched")
	}
	if !strings.Contains(strings.Join(notOnPath.Dirs, ","), filepath.Join(".local", "bin")) {
		t.Errorf("Dirs = %v, want it to include the tool's .local/bin", notOnPath.Dirs)
	}
}

// TestRunInstall_HappyPath verifies the end-to-end success branch: an install command that drops a binary into a tmp dir we then add to PATH succeeds, RunInstall finds the binary via post-install LookPath, and returns nil. Unix-only because the trivial executable creation (`touch + chmod +x`) doesn't translate to cmd.exe.
func TestRunInstall_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only: relies on touch/chmod semantics for the fake installer")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-tool")
	// Prepend the tmp dir to PATH so post-install LookPath finds the binary the installer "creates" below.
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

// TestErrInstalledButNotOnPath_ErrorMessage pins that the typed error's English fallback still carries the ExecName — the cmd/use layer renders a localized message, but library-level errors still need to be debuggable when surfaced raw (logs, %v in stack traces).
func TestErrInstalledButNotOnPath_ErrorMessage(t *testing.T) {
	err := &ErrInstalledButNotOnPath{Tool: &Tool{ExecName: "widget"}}
	if msg := err.Error(); msg == "" {
		t.Fatal("Error() should not be empty")
	}
	if !strings.Contains(err.Error(), "widget") {
		t.Errorf("Error() %q should mention the ExecName", err.Error())
	}
}
