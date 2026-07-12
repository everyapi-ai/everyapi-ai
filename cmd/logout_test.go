package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogoutRejectsExtraArgsBeforeDeletingCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "everyapi", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"access_token":"keep-me"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Logout([]string{"typo"}); err == nil {
		t.Fatal("logout accepted an extra positional")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("invalid logout deleted credentials: %v", err)
	}
}
