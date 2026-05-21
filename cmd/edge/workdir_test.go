package edge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestNodeDir + active pointer round-trip: write, read, clear.
// Uses XDG env overrides to point at a tmp dir so we don't touch the
// user's real ~/.local/share or ~/.config.
func TestActiveNodePointerRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))

	if _, err := activeNodeID(); !errors.Is(err, errNoActiveNode) {
		t.Fatalf("expected errNoActiveNode on fresh tmp, got %v", err)
	}

	if err := setActiveNodeID(42); err != nil {
		t.Fatal(err)
	}
	got, err := activeNodeID()
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Errorf("active = %d, want 42", got)
	}

	// Clear should be a no-op for a non-matching id.
	if err := clearActiveNodeID(7); err != nil {
		t.Fatalf("clearActiveNodeID(7) unexpected error: %v", err)
	}
	if got, _ := activeNodeID(); got != 42 {
		t.Errorf("non-matching clear erased the wrong id: now %d", got)
	}

	// Clear matching id removes the pointer file.
	if err := clearActiveNodeID(42); err != nil {
		t.Fatal(err)
	}
	if _, err := activeNodeID(); !errors.Is(err, errNoActiveNode) {
		t.Errorf("expected errNoActiveNode after matching clear, got %v", err)
	}
}

func TestNodeDirRespectsXDG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	dir, err := nodeDir(123)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, "everyapi", "edge", "123")
	if dir != want {
		t.Errorf("nodeDir = %q, want %q", dir, want)
	}
}

func TestResolveNodeIDExplicitWins(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))

	// No active set; explicit > 0 should still resolve.
	id, err := resolveNodeID(99)
	if err != nil {
		t.Fatal(err)
	}
	if id != 99 {
		t.Errorf("resolveNodeID(99) = %d, want 99", id)
	}

	// Explicit 0 + no active → error.
	if _, err := resolveNodeID(0); !errors.Is(err, errNoActiveNode) {
		t.Errorf("resolveNodeID(0) with no active should be errNoActiveNode, got %v", err)
	}
}

func TestNodeMetaWriteReadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	meta := &nodeMeta{
		NodeID:            42,
		NodeName:          "rtx-4090-tokyo",
		RegistrationToken: "rt_abc",
		Gateway:           "wss://api.everyapi.ai",
		Mode:              ModeNVIDIA,
	}
	if err := saveNodeMeta(42, meta); err != nil {
		t.Fatal(err)
	}
	got, err := loadNodeMeta(42)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeName != "rtx-4090-tokyo" || got.RegistrationToken != "rt_abc" || got.Mode != ModeNVIDIA {
		t.Errorf("loadNodeMeta returned %+v", got)
	}

	// Permission check — node.json must be 0600 (token inside).
	dir, _ := nodeDir(42)
	info, err := os.Stat(filepath.Join(dir, "node.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("node.json perm = %o, want 0600", perm)
	}
}

func TestLoadNodeMetaMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	_, err := loadNodeMeta(999)
	if err == nil {
		t.Fatal("expected error for missing node.json")
	}
}
