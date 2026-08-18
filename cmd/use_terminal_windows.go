//go:build windows

package cmd

import (
	"errors"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

func tmuxAvailable() bool {
	return false
}

func relaunchUseInTmux([]string) error {
	return errors.New(i18n.T("use.tmux_windows_unsupported"))
}

func adoptCurrentTmuxContext() error {
	return errors.New(i18n.T("use.tmux_windows_unsupported"))
}

func RunTmuxUseWrapper() (int, error) {
	return 1, errors.New(i18n.T("use.tmux_windows_unsupported"))
}
