package settings

import (
	"os"
	"testing"
)

// TestMain points the package at a throwaway config dir.
//
// The artifact row reads account state — credentials.json to know which account, settings.json for the
// cached answer — so without this the assertions below are assertions about the machine running them: a
// developer signed in with reports turned off would watch this package fail while CI stayed green.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "everyapi-settings-config-")
	if err != nil {
		panic("create test config dir: " + err.Error())
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic("set XDG_CONFIG_HOME: " + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
