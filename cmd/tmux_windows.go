//go:build windows

package cmd

import (
	"errors"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

// tmux terminal mode does not exist on Windows (see relaunchUseInTmux's stub),
// so there is never a managed session to list, attach or kill. Report the same
// reason `everyapi use` gives rather than an empty list, which would read as
// "your sessions are gone".

func runTmuxDefault() error {
	return errors.New(i18n.T("use.tmux_windows_unsupported"))
}

func runTmuxList([]string) error {
	return errors.New(i18n.T("use.tmux_windows_unsupported"))
}

func runTmuxAttach([]string) error {
	return errors.New(i18n.T("use.tmux_windows_unsupported"))
}

func runTmuxKill([]string) error {
	return errors.New(i18n.T("use.tmux_windows_unsupported"))
}
