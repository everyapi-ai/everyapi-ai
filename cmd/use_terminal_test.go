package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestResolveTerminalModePromptsOnceAndPersists(t *testing.T) {
	settings := &config.Settings{}
	picks := 0
	saves := 0
	mode, err := resolveTerminalMode(settings, true, func() (string, error) {
		picks++
		return config.TerminalModeTmux, nil
	}, func(got *config.Settings) error {
		saves++
		if got.TerminalMode != config.TerminalModeTmux {
			t.Fatalf("saved TerminalMode = %q, want tmux", got.TerminalMode)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if mode != config.TerminalModeTmux || picks != 1 || saves != 1 {
		t.Fatalf("mode=%q picks=%d saves=%d, want tmux,1,1", mode, picks, saves)
	}

	mode, err = resolveTerminalMode(settings, true, func() (string, error) {
		picks++
		return config.TerminalModeNative, nil
	}, func(*config.Settings) error {
		saves++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if mode != config.TerminalModeTmux || picks != 1 || saves != 1 {
		t.Fatalf("remembered mode=%q picks=%d saves=%d, want tmux,1,1", mode, picks, saves)
	}
}

func TestConsumeTmuxUseArgsDecodesOnceAndClearsEnvironment(t *testing.T) {
	want := []string{"claude", "--", ";", "model with spaces"}
	payload, err := encodeTmuxUseArgs(want)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(tmuxUseArgsEnv, payload)
	t.Setenv(tmuxStatusSocketEnv, "/tmp/everyapi-status.sock")
	t.Setenv(tmuxEnvironmentFileEnv, "/tmp/everyapi-environment.json")
	got, err := consumeTmuxUseArgs([]string{tmuxReentryArg})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if _, exists := os.LookupEnv(tmuxUseArgsEnv); exists {
		t.Fatalf("%s remained in the process environment", tmuxUseArgsEnv)
	}
	if _, exists := os.LookupEnv(tmuxStatusSocketEnv); exists {
		t.Fatalf("%s remained in the process environment", tmuxStatusSocketEnv)
	}
	if _, exists := os.LookupEnv(tmuxEnvironmentFileEnv); exists {
		t.Fatalf("%s remained in the process environment", tmuxEnvironmentFileEnv)
	}
}

func TestTmuxPrivateCommandPredicatesRequireExactArguments(t *testing.T) {
	if !IsTmuxUseReentry([]string{tmuxReentryArg}) {
		t.Fatal("exact tmux reentry marker was not recognized")
	}
	for _, args := range [][]string{nil, {"claude"}, {tmuxReentryArg, "claude"}} {
		if IsTmuxUseReentry(args) {
			t.Fatalf("args %#v were recognized as tmux reentry", args)
		}
	}
	if !IsTmuxUseWrapperCommand(tmuxUseWrapperCommand) {
		t.Fatal("private tmux wrapper command was not recognized")
	}
	if IsTmuxUseWrapperCommand("use") {
		t.Fatal("public use command was recognized as the private tmux wrapper")
	}
}

func TestConsumeTmuxUseArgsIgnoresAmbientPayloadWithoutMarker(t *testing.T) {
	t.Setenv(tmuxUseArgsEnv, "stale")
	want := []string{"codex"}
	got, err := consumeTmuxUseArgs(want)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestConsumeTmuxUseArgsRejectsMissingPayload(t *testing.T) {
	t.Setenv(tmuxUseArgsEnv, "")
	if _, err := consumeTmuxUseArgs([]string{tmuxReentryArg}); err == nil {
		t.Fatal("missing tmux payload was accepted")
	}
}

func TestTerminalModePickerOptionsDisableUnavailableTmux(t *testing.T) {
	_, values, disabled := terminalModePickerOptions(false)
	if !reflect.DeepEqual(values, []string{config.TerminalModeNative, config.TerminalModeTmux}) {
		t.Fatalf("values = %#v", values)
	}
	if !reflect.DeepEqual(disabled, []bool{false, true}) {
		t.Fatalf("disabled = %#v, want native enabled and tmux disabled", disabled)
	}
	labels, _, disabled := terminalModePickerOptions(true)
	if labels[0] == "" || labels[1] == "" {
		t.Fatalf("labels = %#v, want both localized labels", labels)
	}
	if !reflect.DeepEqual(disabled, []bool{false, false}) {
		t.Fatalf("disabled with tmux available = %#v", disabled)
	}
}

func TestResolveTerminalModeUsesNativeWithoutPromptOutsideTTY(t *testing.T) {
	settings := &config.Settings{TerminalMode: config.TerminalModeTmux}
	mode, err := resolveTerminalMode(settings, false, func() (string, error) {
		t.Fatal("non-interactive launch prompted")
		return "", nil
	}, func(*config.Settings) error {
		t.Fatal("non-interactive launch saved settings")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if mode != config.TerminalModeNative {
		t.Fatalf("mode = %q, want native", mode)
	}
}

func TestNonInteractiveLaunchDoesNotReadTerminalSettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := config.SettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := maybeRelaunchUseInTerminal([]string{"claude"}); err != nil {
		t.Fatalf("non-interactive launch read terminal settings: %v", err)
	}
}

func TestResolveTerminalModeReturnsSaveFailure(t *testing.T) {
	want := errors.New("disk full")
	_, err := resolveTerminalMode(&config.Settings{}, true, func() (string, error) {
		return config.TerminalModeNative, nil
	}, func(*config.Settings) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
