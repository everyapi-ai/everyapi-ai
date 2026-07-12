package settings

import (
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestLeafCommandsRejectExtraPositionals(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cases := []struct {
		name string
		args []string
	}{
		{"get", []string{"language", "extra"}},
		{"set", []string{"language", "en", "extra"}},
		{"reset", []string{"extra"}},
		{"reset after flag", []string{"-y", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			switch tc.name {
			case "get":
				err = runGet(tc.args)
			case "set":
				err = runSet(tc.args)
			default:
				err = runReset(tc.args)
			}
			if err == nil {
				t.Fatalf("%s accepted extra positional args %v", tc.name, tc.args)
			}
		})
	}
}

func TestResetExtraArgsDoesNotClearSettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveSettings(&config.Settings{Language: "zh"}); err != nil {
		t.Fatal(err)
	}
	if err := runReset([]string{"-y", "extra"}); err == nil {
		t.Fatal("reset accepted an extra positional")
	}
	got, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Language != "zh" {
		t.Fatalf("invalid reset mutated settings: language = %q", got.Language)
	}
}
