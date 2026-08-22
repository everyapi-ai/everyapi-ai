package cmd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const (
	tmuxUseArgsEnv         = "EVERYAPI_TMUX_USE_ARGS"
	tmuxStatusSocketEnv    = "EVERYAPI_TMUX_STATUS_SOCKET"
	tmuxEnvironmentFileEnv = "EVERYAPI_TMUX_ENVIRONMENT_FILE"
	tmuxErrorFileEnv       = "EVERYAPI_TMUX_ERROR_FILE"
	tmuxErrorFileName      = "error.txt"
	tmuxReentryArg         = "--everyapi-tmux-reentry"
	tmuxUseWrapperCommand  = "__tmux-use-wrapper"
)

// tmuxErrorFile is where this process should leave a fatal error so the outer
// EveryAPI process — the one still attached to the user's real terminal — can
// print it. Empty outside a tmux-mode launch.
//
// Everything printed inside the session goes to a pane tmux destroys the instant
// the process exits, which is why the preflight moved every locally knowable
// check ahead of the relaunch. The checks that remain cannot be hoisted: they
// need the network, or a picker, or the tool itself. Their errors were reaching
// the user as an exit code and nothing else. This is the channel that carries
// the words out.
//
// A file rather than the status socket because the socket's contract is one
// integer written by the wrapper after it reaps the child; the message comes from
// the child, one process deeper. The socket write still orders the two: the outer
// process only reads this file after the wrapper has reported, and the wrapper
// only reports after the child exited, so the write always precedes the read.
var tmuxErrorFile string

// tmuxFatalErrorLimit bounds both the write and the read. A relayed backend error
// can be long, and this is a terminal message, not a log.
const tmuxFatalErrorLimit = 4 << 10

// validTmuxErrorFilePath holds the recorded path to the same shape
// validateTmuxRuntimePaths demands of the socket and the environment file: a
// fixed name inside a private per-launch directory under /tmp. That guard runs in
// the wrapper and covers what the wrapper reads; this one covers the write, which
// both the wrapper and the process one level deeper perform straight from the
// environment. An environment that has been tampered with should not be able to
// aim this at an arbitrary file.
func validTmuxErrorFilePath(path string) bool {
	if path == "" || filepath.Base(path) != tmuxErrorFileName {
		return false
	}
	directory := filepath.Dir(path)
	return filepath.Dir(directory) == "/tmp" && strings.HasPrefix(filepath.Base(directory), "everyapi-tmux-")
}

// RecordTmuxFatalError leaves message where the outer process will find it.
// Best-effort by design: failing to record a diagnostic must never change the
// exit status the user gets, and the caller is already on its way out.
func RecordTmuxFatalError(message string) {
	if message == "" || !validTmuxErrorFilePath(tmuxErrorFile) {
		return
	}
	if len(message) > tmuxFatalErrorLimit {
		message = message[:tmuxFatalErrorLimit]
	}
	_ = os.WriteFile(tmuxErrorFile, []byte(message), 0o600)
}

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
	// Captured before it is cleared: the path has to outlive this call so a fatal
	// error raised anywhere further down the launch can still be written where the
	// outer process will look, while the variable itself must not survive into the
	// tool's environment.
	tmuxErrorFile = os.Getenv(tmuxErrorFileEnv)
	_ = os.Unsetenv(tmuxUseArgsEnv)
	_ = os.Unsetenv(tmuxStatusSocketEnv)
	_ = os.Unsetenv(tmuxEnvironmentFileEnv)
	_ = os.Unsetenv(tmuxErrorFileEnv)
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

// relaunchUseInTerminal is the tmux boundary as a package var so tests can
// observe whether a launch crossed it, and in what order relative to
// usePreflight. Production always points at maybeRelaunchUseInTerminal.
var relaunchUseInTerminal = maybeRelaunchUseInTerminal

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
