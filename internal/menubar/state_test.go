package menubar

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadState(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Missing file returns zero state, no error.
	got, err := loadState()
	if err != nil {
		t.Fatalf("loadState (missing): %v", err)
	}
	if got != (persistedState{}) {
		t.Errorf("loadState (missing) = %+v, want zero", got)
	}

	want := persistedState{SanitizerEnabled: true, SanitizerListen: "127.0.0.1:9999"}
	if err := saveState(want); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	got, err = loadState()
	if err != nil {
		t.Fatalf("loadState (present): %v", err)
	}
	if got != want {
		t.Errorf("loadState = %+v, want %+v", got, want)
	}

	// File should be mode 0600 — credentials adjacent, same posture.
	path, _ := statePath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestLoadState_CorruptFile(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), "everyapi")
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(cfgDir))
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path, _ := statePath()
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadState()
	if err == nil {
		t.Error("expected error on corrupt file, got nil")
	}
}
