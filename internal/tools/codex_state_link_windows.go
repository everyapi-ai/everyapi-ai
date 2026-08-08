//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func linkCodexStateDirectory(target, link string) error {
	symlinkErr := os.Symlink(target, link)
	if symlinkErr == nil {
		return nil
	}
	const script = `$ErrorActionPreference = 'Stop'
$target = [Environment]::GetEnvironmentVariable('EVERYAPI_CODEX_JUNCTION_TARGET', 'Process')
$link = [Environment]::GetEnvironmentVariable('EVERYAPI_CODEX_JUNCTION_LINK', 'Process')
New-Item -ItemType Junction -Path $link -Target $target | Out-Null`
	cmd := exec.Command(
		"powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive",
		"-Command", script,
	)
	cmd.Env = mergeEnv(map[string]string{
		"EVERYAPI_CODEX_JUNCTION_TARGET": target,
		"EVERYAPI_CODEX_JUNCTION_LINK":   link,
	})
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"create directory symlink (%v) or junction: %w (%s)",
			symlinkErr, err, strings.TrimSpace(string(output)),
		)
	}
	return nil
}
