package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/tools"
)

// InstallTool is the desktop-only half of the install/launch split. The tool
// name resolves through the compile-time registry, and RunInstall executes only
// that entry's fixed InstallCmd. It never calls Use or starts the installed
// client.
func InstallTool(args []string) error {
	if len(args) != 1 {
		return errors.New("desktop-install-tool requires exactly one tool")
	}
	tool, err := tools.Lookup(args[0])
	if err != nil {
		return err
	}
	if tools.IsInstalled(tool) {
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
	cliout.Printf("  %s\n", tool.InstallCmd)
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
