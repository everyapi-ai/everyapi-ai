package tools

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
