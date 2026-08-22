package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
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

func TestShouldReuseTmuxSessionOnlyForBareCodexResume(t *testing.T) {
	tests := []struct {
		name    string
		useArgs []string
		want    bool
	}{
		{name: "picker", useArgs: []string{"codex", "--", "resume"}, want: true},
		{name: "global picker", useArgs: []string{"codex", "--", "resume", "--all"}, want: true},
		{name: "channel override", useArgs: []string{"codex", "--channel", "new", "--", "resume"}, want: false},
		{name: "model override", useArgs: []string{"--model", "gpt-5", "codex", "--", "resume"}, want: false},
		{name: "transparent override", useArgs: []string{"codex", "--transparent=false", "--", "resume"}, want: false},
		{name: "sanitizer override", useArgs: []string{"codex", "--sanitize=false", "--", "resume"}, want: false},
		{name: "last", useArgs: []string{"codex", "--", "resume", "--last"}, want: false},
		{name: "specific", useArgs: []string{"codex", "--", "resume", "session-id"}, want: false},
		{name: "new codex", useArgs: []string{"codex"}, want: false},
		{name: "codex help", useArgs: []string{"codex", "--", "help"}, want: false},
		{name: "other tool", useArgs: []string{"claude", "--", "resume"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldReuseTmuxSession(tt.useArgs); got != tt.want {
				t.Fatalf("shouldReuseTmuxSession(%#v) = %v, want %v", tt.useArgs, got, tt.want)
			}
		})
	}
}

// writeTestCredentials plants a minimal legacy-flow credentials file in the
// XDG config dir the caller has already redirected to a temp directory.
func writeTestCredentials(t *testing.T, credentials string) {
	t.Helper()
	dir, err := config.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(credentials), 0o600); err != nil {
		t.Fatal(err)
	}
}

// stubTerminalRelaunch replaces the tmux boundary and reports whether a launch
// crossed it.
func stubTerminalRelaunch(t *testing.T) *bool {
	t.Helper()
	crossed := false
	previous := relaunchUseInTerminal
	t.Cleanup(func() { relaunchUseInTerminal = previous })
	relaunchUseInTerminal = func([]string) error {
		crossed = true
		return nil
	}
	return &crossed
}

// A failure the outer process can see locally must be reported there. Crossing
// into tmux first writes it to a pane tmux destroys on exit, so the user gets
// the launch banner, "[exited]", and a dead reattach hint instead of the one
// line that says what to fix.
func TestUseRejectsKnownFailuresBeforeCrossingTheTmuxBoundary(t *testing.T) {
	const validCredentials = `{"api_base":"https://api.everyapi.ai","access_token":"t","relay_key":"sk-everyapi-test"}`
	const oauthCredentials = `{"api_base":"https://api.everyapi.ai","relay_key":"sk-everyapi-test","oauth_client_id":"client"}`
	tests := []struct {
		name        string
		credentials string
		useArgs     []string
		wantError   string
	}{
		{name: "logged out", useArgs: []string{"codex"}, wantError: i18n.T("auth.not_logged_in")},
		{name: "unknown tool", credentials: validCredentials, useArgs: []string{"nosuchtool"}, wantError: `unknown tool "nosuchtool"`},
		{name: "relay key group", credentials: oauthCredentials, useArgs: []string{"codex", "--group", "g"}, wantError: i18n.T("use.relay_key_mode_group")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if test.credentials != "" {
				writeTestCredentials(t, test.credentials)
			}
			crossed := stubTerminalRelaunch(t)
			err := use(test.useArgs, true)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want it to contain %q", err, test.wantError)
			}
			if *crossed {
				t.Fatal("launch crossed the tmux boundary before reporting a locally knowable failure")
			}
		})
	}
}

// The happy path must still reach the relaunch: the preflight is a gate, not a
// replacement for it.
func TestUsePreflightPassesThroughToTheTerminalRelaunch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeTestCredentials(t, `{"api_base":"https://api.everyapi.ai","access_token":"t","relay_key":"sk-everyapi-test"}`)
	creds, tool, err := usePreflight("codex", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if creds == nil || creds.RelayKey != "sk-everyapi-test" {
		t.Fatalf("credentials = %+v, want the planted relay key", creds)
	}
	if tool == nil || tool.ExecName != "codex" {
		t.Fatalf("tool = %+v, want codex", tool)
	}
}
