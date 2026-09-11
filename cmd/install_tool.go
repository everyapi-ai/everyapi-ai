package cmd

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

// InstallTool is the desktop-only half of the install/launch split. The tool name resolves through the compile-time registry, and RunInstall executes only that entry's fixed InstallCmd. The optional --force flag lets Connect reuse the same allowlisted installer for an update when the executable already exists. It never calls Use or starts the installed client.
func InstallTool(args []string) error {
	force := len(args) == 2 && args[1] == "--force"
	if len(args) != 1 && !force {
		return errors.New("desktop-install-tool requires one tool and optional --force")
	}
	tool, err := tools.Lookup(args[0])
	if err != nil {
		return err
	}
	// The desktop updater must operate on Claude's native installation instead of whichever older shim appears first in the GUI process's PATH. Its errors go through the same translation as every other path below: this branch used to return the raw error, which is why a Connect-initiated Claude update was the one install flow that reported raw Go plumbing instead of an actionable message.
	//
	// The prerequisite preflight applies only when Claude is NOT already installed. An existing installation updates itself with `claude update` and needs none of the installer's prerequisites; gating that unconditionally would abort a working update with "claude is not installed, but its installer needs curl" — a statement that is false on its face and sends the reader at the wrong subsystem.
	if force && tool.ExecName == "claude" {
		if !tools.IsInstalled(tool) {
			if err := installPreflight(tool); err != nil {
				return err
			}
		}
		return describeInstallError(runClaudeDesktopUpdate())
	}
	if tools.IsInstalled(tool) && !force {
		cliout.Printf(i18n.T("use.installed")+"\n", tool.Name)
		return nil
	}
	if err := installPreflight(tool); err != nil {
		return err
	}
	cliout.Printf(i18n.T("use.tool_not_installed")+"\n", tool.ExecName)
	cliout.Printf("  %s\n", tools.InstallCommand(tool))
	cliout.Printf(i18n.T("use.installing")+"\n", tool.Name)
	if err := describeInstallError(tools.RunInstall(tool)); err != nil {
		return err
	}
	cliout.Printf(i18n.T("use.installed")+"\n", tool.Name)
	return nil
}

// describeInstallError translates the structured failures RunInstall raises into localized, actionable messages, and passes anything else through untouched. Shared by every install entry point so no branch can quietly leak a Go error string the way the Claude update branch used to.
func describeInstallError(err error) error {
	if err == nil {
		return nil
	}
	var notOnPath *tools.ErrInstalledButNotOnPath
	if errors.As(err, &notOnPath) {
		if len(notOnPath.Dirs) > 0 {
			return fmt.Errorf(i18n.T("use.installed_not_on_path_dirs"),
				notOnPath.Tool.ExecName, strings.Join(notOnPath.Dirs, ", "))
		}
		return fmt.Errorf(i18n.T("use.installed_not_on_path"), notOnPath.Tool.ExecName)
	}
	var blocked *tools.ErrInstallerExecBlocked
	if errors.As(err, &blocked) {
		return fmt.Errorf(i18n.T("use.installer_exec_blocked"),
			blocked.Tool.ExecName, blocked.Executable, tools.InstallCommand(blocked.Tool))
	}
	return err
}

// installPreflight runs the checks every install path shares: the tool must have a reviewed installer for this platform, and that installer's prerequisite must be resolvable. A missing npm does not stop here — RunInstall bootstraps Node for it — so this gates only what no amount of downloading can fix.
func installPreflight(tool *tools.Tool) error {
	if !tools.CanAutoInstall(tool) {
		return &tools.ErrToolNotFound{Tool: tool}
	}
	if missing := tools.InstallerMissing(tool); missing != "" {
		return fmt.Errorf(i18n.T(installerMissingKey()), tool.ExecName, missing, tool.InstallHint)
	}
	return nil
}

// installerMissingKey selects the platform-appropriate wording. The cross-platform message talks about login shells, shell rc files and shell functions, none of which exist on Windows, where the installer runs through cmd.exe and the real cause is almost always that PATH changed after the current process started.
func installerMissingKey() string {
	if runtime.GOOS == "windows" {
		return "use.installer_missing_windows"
	}
	return "use.installer_missing"
}
