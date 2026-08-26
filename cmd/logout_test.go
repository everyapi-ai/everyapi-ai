package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
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

// The fixed per-tool homes are no longer where a launch puts the key: `everyapi use` takes the live-catalog path, which mints a process-scoped home under the prepared-session root instead, and two adapters inline the relay key verbatim in there. Nothing else reclaims those homes on a logout — the only other sweep is age-gated behind the NEXT launch — so if logout misses the root a working, billable credential simply stays on disk.
//
// This drives the real hermes preparer rather than hand-seeding a path, so the sessions root is derived from what the launcher actually produced: move the root and this test fails instead of quietly asserting that logout scrubbed a directory nobody writes to.
func TestLogoutScrubsPreparedSessionHomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// The launcher resolves the boot model from the live catalog and hands it to the preparer through this variable; hermes refuses to write config.yaml without one.
	t.Setenv("EVERYAPI_HERMES_MODEL", "gpt-5.1")

	hermes, err := tools.Lookup("hermes")
	if err != nil {
		t.Fatal(err)
	}
	// A non-empty model slice is what sends hermes down the prepared-home branch; an empty one falls back to the fixed hermes-home the old scrub already covered.
	env, err := hermes.PrepareWithModels("https://gateway.example.test", "sk-everyapi-billable", []tools.Model{{ID: "gpt-5.1"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	hermesHome := env["HERMES_HOME"]
	if hermesHome == "" {
		t.Fatal("hermes prepare returned no HERMES_HOME")
	}
	hermesConfig := filepath.Join(hermesHome, "config.yaml")
	body, err := os.ReadFile(hermesConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "sk-everyapi-billable") {
		t.Fatalf("hermes no longer inlines the relay key, so this test is asserting against the wrong writer:\n%s", body)
	}
	sessions := filepath.Dir(hermesHome)

	// Cline is the second adapter that persists the raw key into a prepared home. Seeded by hand rather than driven: its preparer needs a live catalog whose entries match the endpoint types it filters on, and the point here is that the whole root goes, not one adapter's file layout.
	cline := filepath.Join(sessions, "cline-d4e5f6", "settings", "providers.json")
	if err := os.MkdirAll(filepath.Dir(cline), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cline, []byte(`{"providers":{"everyapi":{"settings":{"apiKey":"sk-everyapi-billable"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Logout(nil); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{hermesConfig, cline} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("a billable relay key outlived logout at %s (stat err: %v)", path, err)
		}
	}
}
