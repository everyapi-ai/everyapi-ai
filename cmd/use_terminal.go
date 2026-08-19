package cmd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const (
	tmuxUseArgsEnv         = "EVERYAPI_TMUX_USE_ARGS"
	tmuxStatusSocketEnv    = "EVERYAPI_TMUX_STATUS_SOCKET"
	tmuxEnvironmentFileEnv = "EVERYAPI_TMUX_ENVIRONMENT_FILE"
	tmuxReentryArg         = "--everyapi-tmux-reentry"
	tmuxUseWrapperCommand  = "__tmux-use-wrapper"
)

func IsTmuxUseReentry(args []string) bool {
	return len(args) == 1 && args[0] == tmuxReentryArg
}

func IsTmuxUseWrapperCommand(name string) bool {
	return name == tmuxUseWrapperCommand
}

func encodeTmuxUseArgs(args []string) (string, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode tmux launch arguments: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeTmuxUseArgs(payload string) ([]string, error) {
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("decode tmux launch arguments: %w", err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		return nil, fmt.Errorf("parse tmux launch arguments: %w", err)
	}
	return args, nil
}

func consumeTmuxUseArgs(args []string) ([]string, error) {
	if len(args) == 0 || args[0] != tmuxReentryArg {
		return args, nil
	}
	if !IsTmuxUseReentry(args) {
		return nil, errors.New("invalid tmux reentry arguments")
	}
	payload := os.Getenv(tmuxUseArgsEnv)
	_ = os.Unsetenv(tmuxUseArgsEnv)
	_ = os.Unsetenv(tmuxStatusSocketEnv)
	_ = os.Unsetenv(tmuxEnvironmentFileEnv)
	if payload == "" {
		return nil, errors.New("tmux reentry payload is missing")
	}
	return decodeTmuxUseArgs(payload)
}

func resolveTerminalMode(settings *config.Settings, interactive bool, pick func() (string, error), save func(*config.Settings) error) (string, error) {
	if !interactive {
		return config.TerminalModeNative, nil
	}
	if settings == nil {
		settings = &config.Settings{}
	}
	switch settings.TerminalMode {
	case config.TerminalModeNative, config.TerminalModeTmux:
		return settings.TerminalMode, nil
	case "":
	default:
		return "", fmt.Errorf("invalid terminal_mode %q; use `everyapi settings set terminal_mode native|tmux`", settings.TerminalMode)
	}
	mode, err := pick()
	if err != nil {
		return "", err
	}
	if mode != config.TerminalModeNative && mode != config.TerminalModeTmux {
		return "", fmt.Errorf("terminal picker returned invalid mode %q", mode)
	}
	settings.TerminalMode = mode
	if err := save(settings); err != nil {
		return "", err
	}
	return mode, nil
}

func shouldRelaunchInTmux(mode, tmuxEnvironment string) bool {
	return mode == config.TerminalModeTmux && tmuxEnvironment == ""
}

func shouldReuseTmuxSession(useArgs []string) bool {
	return len(useArgs) == 3 && useArgs[0] == "codex" && useArgs[1] == "--" && useArgs[2] == "resume" ||
		len(useArgs) == 4 && useArgs[0] == "codex" && useArgs[1] == "--" && useArgs[2] == "resume" && useArgs[3] == "--all"
}

func terminalModePickerOptions(tmuxIsAvailable bool) (labels, values []string, disabled []bool) {
	labels = []string{i18n.T("settings.terminal_mode_native"), i18n.T("settings.terminal_mode_tmux")}
	if !tmuxIsAvailable {
		labels[1] += " " + i18n.T("use.tmux_unavailable_option")
	}
	values = []string{config.TerminalModeNative, config.TerminalModeTmux}
	disabled = []bool{false, !tmuxIsAvailable}
	return labels, values, disabled
}

func pickTerminalMode() (string, error) {
	labels, values, disabled := terminalModePickerOptions(tmuxAvailable())
	idx, err := cliprompt.PickWithDisabled(i18n.T("use.terminal_mode_prompt"), labels, disabled, 0)
	if err != nil {
		return "", err
	}
	return values[idx], nil
}

func maybeRelaunchUseInTerminal(useArgs []string) error {
	if !cliprompt.IsInteractive() {
		return nil
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	mode, err := resolveTerminalMode(settings, true, pickTerminalMode, config.SaveSettings)
	if err != nil {
		return err
	}
	return applyTerminalMode(mode, useArgs)
}

func applyTerminalMode(mode string, useArgs []string) error {
	if !shouldRelaunchInTmux(mode, os.Getenv("TMUX")) {
		if mode == config.TerminalModeTmux && os.Getenv("TMUX") != "" {
			return adoptCurrentTmuxContext()
		}
		return nil
	}
	return relaunchUseInTmux(useArgs)
}
