package tools

import (
	"os"
	"testing"
)

// TestMain points the whole package at a throwaway config dir.
//
// Launch tests write settings.json, and more settings will land on this path.
// Without this, those assertions are really assertions
// about the machine running the tests: a developer who turns the report off for
// their own sessions would watch this package fail, and CI would still be green.
// Tests that need particular settings write them into their own temp dir with
// t.Setenv, which still overrides this.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "everyapi-tools-config-")
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
