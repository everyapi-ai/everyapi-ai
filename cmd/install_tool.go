package cmd

import (
	"errors"
	"fmt"
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
	// The desktop updater must operate on Claude's native installation instead of whichever older shim appears first in the GUI process's PATH.
	if force && tool.ExecName == "claude" {
		return runClaudeDesktopUpdate()
	}
	if tools.IsInstalled(tool) && !force {
		cliout.Printf(i18n.T("use.installed")+"\n", tool.Name)
		return nil
	}
	if !tools.CanAutoInstall(tool) {
		return &tools.ErrToolNotFound{Tool: tool}
	}
	if missing := tools.InstallerMissing(tool); missing != "" {
		return fmt.Errorf(i18n.T("use.installer_missing"), tool.ExecName, missing, tool.InstallHint)
	}
	cliout.Printf(i18n.T("use.tool_not_installed")+"\n", tool.ExecName)
	cliout.Printf("  %s\n", tools.InstallCommand(tool))
	cliout.Printf(i18n.T("use.installing")+"\n", tool.Name)
	if err := tools.RunInstall(tool); err != nil {
		var notOnPath *tools.ErrInstalledButNotOnPath
		if errors.As(err, &notOnPath) {
			if len(notOnPath.Dirs) > 0 {
				return fmt.Errorf(i18n.T("use.installed_not_on_path_dirs"),
					notOnPath.Tool.ExecName, strings.Join(notOnPath.Dirs, ", "))
			}
			return fmt.Errorf(i18n.T("use.installed_not_on_path"), notOnPath.Tool.ExecName)
		}
		return err
	}
	cliout.Printf(i18n.T("use.installed")+"\n", tool.Name)
	return nil
}
