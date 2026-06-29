package tools

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// IsInstalled reports whether the tool's ExecName is on the current
// $PATH. Mirrors the LookPath used by Exec so the preflight check
// matches what Exec would see a moment later.
func IsInstalled(t *Tool) bool {
	if t == nil {
		return false
	}
	_, err := exec.LookPath(t.ExecName)
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
// re-checks $PATH; an exit-0 install that still leaves the binary
// unfindable (npm's global bin not on PATH is the common case)
// surfaces as an actionable error instead of letting the caller
// re-exec into a still-missing tool.
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
	} else {
		cmd = exec.Command("sh", "-c", t.InstallCmd)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install %s: %w", t.Name, err)
	}
	if !IsInstalled(t) {
		return &ErrInstalledButNotOnPath{Tool: t}
	}
	return nil
}

// ErrInstalledButNotOnPath signals that the install command exited
// cleanly but ExecName still isn't on PATH — the canonical example
// is `npm install -g …` succeeding while the user's shell doesn't
// have npm's global bin directory on PATH. The error message tells
// the user to open a new shell or fix their PATH instead of letting
// them re-run `everyapi use` in the same shell and hit the same
// "not installed" wall.
type ErrInstalledButNotOnPath struct {
	Tool *Tool
}

func (e *ErrInstalledButNotOnPath) Error() string {
	return fmt.Sprintf(
		"%s installed but not on $PATH yet. Open a new shell, or add the installer's bin directory to PATH.",
		e.Tool.ExecName,
	)
}
