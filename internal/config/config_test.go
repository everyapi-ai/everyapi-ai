package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveLoadRoundTrip writes credentials, reads them back, and
// confirms every field survives the JSON round-trip — guards against
// future schema renames silently dropping fields.
func TestSaveLoadRoundTrip(t *testing.T) {
	// Redirect XDG_CONFIG_HOME at a temp dir so the test never
	// touches a real user's credentials file. ConfigDir() honors
	// this variable first.
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	want := &Credentials{
		APIBase:     "http://localhost:8787",
		AccessToken: "rl_abcdef1234567890abcdef1234567890",
		RelayKey:    "sk-everyapi-abcdef1234567890",
		UserID:      4242,
		Username:    "test-user",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File should exist at the expected path with mode 0600.
	path := filepath.Join(tmp, "everyapi", "credentials.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %o, want 0600", info.Mode().Perm())
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != *want {
		t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", want, got)
	}
}

// TestLoad_Missing returns the ErrNoCredentials sentinel so callers
// can render "run 'everyapi login' first" rather than a raw file error.
func TestLoad_Missing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	_, err := Load()
	if err != ErrNoCredentials {
		t.Fatalf("want ErrNoCredentials, got %v", err)
	}
}

// TestLoad_AppliesAPIBaseDefault — a credentials file written by an
// older CLI without the api_base field should still come back with
// the production URL filled in, so api.New(creds.APIBase, …) doesn't
// hit an empty URL.
func TestLoad_AppliesAPIBaseDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dir := filepath.Join(tmp, "everyapi")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Hand-rolled JSON, missing api_base.
	legacy := []byte(`{"access_token":"tok","user_id":1,"username":"u"}`)
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.APIBase != DefaultAPIBase {
		t.Errorf("APIBase = %q, want default %q", got.APIBase, DefaultAPIBase)
	}
}

// TestDelete_Idempotent: a fresh logout without prior login (or two
// consecutive logouts) must not error.
func TestDelete_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	if err := Delete(); err != nil {
		t.Errorf("Delete on missing file: %v", err)
	}
	if err := Save(&Credentials{APIBase: "https://x", AccessToken: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(); err != nil {
		t.Errorf("Delete on existing file: %v", err)
	}
	if err := Delete(); err != nil {
		t.Errorf("Delete again (already removed): %v", err)
	}
}
