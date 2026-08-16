package tools

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// IsInstalled reports whether the tool's executable can be found — on $PATH, in the tool's own ExtraBinDirs, or (for npm-installed tools) in npm's global bin directory, even when those dirs aren't on $PATH. Delegates to ResolveExec so the preflight check matches exactly what Exec will resolve a moment later, which is what keeps `everyapi use` from looping on "not installed" after an install whose output dir never made it onto PATH.
func IsInstalled(t *Tool) bool {
	if t == nil {
		return false
	}
	_, err := ResolveExec(t)
	return err == nil
}

// CanAutoInstall reports whether RunInstall will actually attempt anything for this tool on the current platform. Used by callers (cmd/use) to decide between "offer the install prompt" and "fall back to the ErrToolNotFound hint".
func CanAutoInstall(t *Tool) bool {
	return !installCommandForOS(t, runtime.GOOS).empty()
}

type installCommandSpec struct {
	shell      string
	executable string
	args       []string
}

func (command installCommandSpec) empty() bool {
	return command.shell == "" && command.executable == ""
}

func (command installCommandSpec) display() string {
	if command.executable == "" {
		return command.shell
	}
	parts := make([]string, 0, len(command.args)+1)
	parts = append(parts, command.executable)
	for _, arg := range command.args {
		if strings.ContainsAny(arg, " \t\r\n|&<>") {
			parts = append(parts, fmt.Sprintf("%q", arg))
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}

func installCommandForOS(t *Tool, goos string) installCommandSpec {
	if t == nil {
		return installCommandSpec{}
	}
	if goos == "windows" {
		if len(t.InstallCmdWindows) > 0 && t.InstallCmdWindows[0] != "" {
			return installCommandSpec{
				executable: t.InstallCmdWindows[0],
				args:       append([]string(nil), t.InstallCmdWindows[1:]...),
			}
		}
		if t.InstallCmdUnixOnly {
			return installCommandSpec{}
		}
	}
	return installCommandSpec{shell: t.InstallCmd}
}

// InstallCommand returns the exact platform-selected installer in a human-readable form for terminal audit output. RunInstall consumes the same selection, but executes native Windows argv without reparsing this string.
func InstallCommand(t *Tool) string {
	return installCommandForOS(t, runtime.GOOS).display()
}

// InstallerMissing reports the executable the platform-selected installer needs in order to run ("npm" for `npm install -g …`, "curl" for `curl … | bash`, or the first structured Windows argv element) when it is NOT resolvable on $PATH; it returns "" when the command is present, or when the tool has no auto-installer to gate.
//
// Why this exists: RunInstall shells the InstallCmd out through a non-interactive `sh -c`, which does NOT source the user's ~/.zshrc / ~/.bashrc. A Node version manager (nvm/fnm/volta) commonly exposes `npm` only as a shell function or via a PATH entry added in those rc files, so a user whose interactive shell "has npm" can still hand RunInstall a shell where `npm` resolves to nothing — yielding a cryptic "npm: command not found" (exit 127). Detecting it up front lets the caller print an actionable message instead.
func InstallerMissing(t *Tool) string {
	req := installRequires(t)
	if req == "" {
		return ""
	}
	if _, err := exec.LookPath(req); err == nil {
		return ""
	}
	return req
}

// installRequires returns the executable selected for the current platform, or "" when no installer is available. Installer commands are compile-time literals (see the SECURITY INVARIANT on Tool.InstallCmd), so this is a stable, trustworthy command name.
func installRequires(t *Tool) string {
	command := installCommandForOS(t, runtime.GOOS)
	if command.executable != "" {
		return command.executable
	}
	if command.shell == "" {
		return ""
	}
	fields := strings.Fields(command.shell)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func buildInstallCommand(command installCommandSpec, goos string) *exec.Cmd {
	if goos == "windows" && command.executable != "" {
		return exec.Command(command.executable, command.args...)
	}
	if goos == "windows" {
		return exec.Command("cmd", "/C", command.shell)
	}
	if bash, err := exec.LookPath("bash"); err == nil {
		return exec.Command(bash, "-c", "set -o pipefail; "+command.shell)
	}
	return exec.Command("sh", "-c", command.shell)
}

// RunInstall executes the tool's platform-selected installer, streaming stdout/stderr/stdin so npm/curl/PowerShell progress reaches the user's terminal live. After the process returns, it re-checks via ResolveExec; an exit-0 install that still leaves the binary unfindable — on $PATH, in the tool's ExtraBinDirs, or in npm's global bin dir — surfaces as an actionable error (naming the dirs searched) instead of letting the caller re-exec into a still-missing tool.
//
// On Windows, structured InstallCmdWindows argv executes directly; simple cross-platform InstallCmd values such as npm still use `cmd /C`. POSIX-only pipelines are gated by CanAutoInstall.
func RunInstall(t *Tool) error {
	command := installCommandForOS(t, runtime.GOOS)
	if command.empty() {
		return fmt.Errorf("no auto-install available for %s", t.Name)
	}
	// Unix shell installers prefer bash with pipefail so a failed download cannot masquerade as success. Native Windows installers retain their structured argv and never pass nested quoting through cmd.exe.
	cmd := buildInstallCommand(command, runtime.GOOS)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install %s: %w", t.Name, err)
	}
	if _, searched, err := resolveExecDirs(t); err != nil {
		return &ErrInstalledButNotOnPath{Tool: t, Dirs: searched}
	}
	return nil
}

// ErrInstalledButNotOnPath signals that the install command exited cleanly but the executable still can't be resolved — not on $PATH and not in any fallback directory we know to search. The canonical example is `npm install -g …` succeeding while npm's global bin directory is on neither $PATH nor a version-manager env var; the same shape applies to an installer with a fixed output dir (Antigravity writes ~/.local/bin). Dirs carries the directories that WERE searched — the tool's ExtraBinDirs plus, for npm tools, the npm global candidates — so the message can point the user at a concrete place to add to PATH instead of guessing.
type ErrInstalledButNotOnPath struct {
	Tool *Tool
	Dirs []string
}

func (e *ErrInstalledButNotOnPath) Error() string {
	if len(e.Dirs) > 0 {
		return fmt.Sprintf(
			"%s installed but not found on $PATH or in its known install directories (searched: %s). "+
				"Add the directory holding the binary to PATH, or open a new shell.",
			e.Tool.ExecName, strings.Join(e.Dirs, ", "),
		)
	}
	return fmt.Sprintf(
		"%s installed but not on $PATH yet. Open a new shell, or add the installer's bin directory to PATH.",
		e.Tool.ExecName,
	)
}
