package tools

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// IsInstalled reports whether the tool's executable can be found — on
// $PATH, in the tool's own ExtraBinDirs, or (for npm-installed tools) in
// npm's global bin directory, even when those dirs aren't on $PATH.
// Delegates to ResolveExec so the preflight check matches exactly what
// Exec will resolve a moment later, which is what keeps `everyapi use`
// from looping on "not installed" after an install whose output dir never
// made it onto PATH.
func IsInstalled(t *Tool) bool {
	if t == nil {
		return false
	}
	_, err := ResolveExec(t)
	return err == nil
}

// CanAutoInstall reports whether RunInstall will actually attempt
// anything for this tool on the current platform. Used by callers
// (cmd/use) to decide between "offer the install prompt" and
// "fall back to the ErrToolNotFound hint".
func CanAutoInstall(t *Tool) bool {
	if t == nil || t.InstallCmd == "" {
		return false
	}
	if t.InstallCmdUnixOnly && runtime.GOOS == "windows" {
		return false
	}
	return true
}

// InstallerMissing reports the command the tool's InstallCmd needs in
// order to run (the leading word of InstallCmd — "npm" for `npm install
// -g …`, "curl" for `curl … | bash`) when that command is NOT resolvable
// on $PATH; it returns "" when the command is present, or when the tool
// has no auto-installer to gate.
//
// Why this exists: RunInstall shells the InstallCmd out through a
// non-interactive `sh -c`, which does NOT source the user's ~/.zshrc /
// ~/.bashrc. A Node version manager (nvm/fnm/volta) commonly exposes
// `npm` only as a shell function or via a PATH entry added in those rc
// files, so a user whose interactive shell "has npm" can still hand
// RunInstall a shell where `npm` resolves to nothing — yielding a
// cryptic "npm: command not found" (exit 127). Detecting it up front
// lets the caller print an actionable message instead.
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

// installRequires returns the leading command word of the tool's
// InstallCmd (the binary that must be on $PATH for the installer to
// run), or "" when there's no InstallCmd. InstallCmd is a compile-time
// literal (see the SECURITY INVARIANT on Tool.InstallCmd), so its first
// field is a stable, trustworthy command name.
func installRequires(t *Tool) string {
	if t == nil || t.InstallCmd == "" {
		return ""
	}
	fields := strings.Fields(t.InstallCmd)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// RunInstall executes the tool's InstallCmd through the platform
// shell, streaming stdout/stderr/stdin so npm/curl/bash progress
// reaches the user's terminal live. After the shell returns, it
// re-checks via ResolveExec; an exit-0 install that still leaves the
// binary unfindable — on $PATH, in the tool's ExtraBinDirs, or in npm's
// global bin dir — surfaces as an actionable error (naming the dirs
// searched) instead of letting the caller re-exec into a still-missing
// tool.
//
// On Windows the command is passed to `cmd /C`. Pipelines like
// `curl … | bash` won't work there, so callers should gate this
// with CanAutoInstall first.
func RunInstall(t *Tool) error {
	if !CanAutoInstall(t) {
		return fmt.Errorf("no auto-install available for %s", t.Name)
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", t.InstallCmd)
	} else if bash, err := exec.LookPath("bash"); err == nil {
		// Prefer bash with pipefail so a failing pipeline stage is
		// reported. The curl|bash installers exit with bash's status,
		// not curl's — a failed `curl -fsSL … | bash` (network error,
		// HTTP 4xx/5xx) writes nothing and bash exits 0, which without
		// pipefail masquerades as a successful install and misleads the
		// user into chasing a nonexistent $PATH problem. bash is present
		// whenever these curl|bash installers could run at all.
		cmd = exec.Command(bash, "-c", "set -o pipefail; "+t.InstallCmd)
	} else {
		cmd = exec.Command("sh", "-c", t.InstallCmd)
	}
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

// ErrInstalledButNotOnPath signals that the install command exited
// cleanly but the executable still can't be resolved — not on $PATH and
// not in any fallback directory we know to search. The canonical example
// is `npm install -g …` succeeding while npm's global bin directory is on
// neither $PATH nor a version-manager env var; the same shape applies to
// an installer with a fixed output dir (Antigravity writes ~/.local/bin).
// Dirs carries the directories that WERE searched — the tool's
// ExtraBinDirs plus, for npm tools, the npm global candidates — so the
// message can point the user at a concrete place to add to PATH instead
// of guessing.
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
