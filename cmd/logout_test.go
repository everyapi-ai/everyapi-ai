package cmd

import (
	"os"
	"path/filepath"
	"strings"
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

// Logout has to reach the credentials clients own as well as the homes EveryAPI owns. Without this the scrub can exist, be correct, and never be called.
func TestLogoutScrubsCredentialsPersistedInsideClientHomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	credentials := filepath.Join(home, ".dsh", ".credentials.yaml")
	if err := os.MkdirAll(filepath.Dir(credentials), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentials, []byte("ANTHROPIC_API_KEY: user-own\nEVERYAPI_API_KEY: sk-everyapi-billable\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Logout(nil); err != nil {
		t.Fatal(err)
	}

	remaining, err := os.ReadFile(credentials)
	if err != nil {
		t.Fatalf("logout removed a file holding the user's own credentials: %v", err)
	}
	if strings.Contains(string(remaining), "sk-everyapi-billable") {
		t.Errorf("a billable relay key outlived logout:\n%s", remaining)
	}
	if !strings.Contains(string(remaining), "ANTHROPIC_API_KEY: user-own") {
		t.Errorf("logout discarded a credential that is not ours:\n%s", remaining)
	}
}
