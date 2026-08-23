package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManagedBlockRoundTripPreservesUserContent is the property the whole mechanism has to hold: this writes into a file the USER owns, so a launch followed by an exit must leave the file byte-identical to what it was.
func TestManagedBlockRoundTripPreservesUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".goosehints")
	original := "# my own hints\n\n- prefer tabs\n- no emoji\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeManagedBlock(path, "injected instructions"); err != nil {
		t.Fatal(err)
	}
	patched, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patched), "injected instructions") {
		t.Fatalf("block was not written:\n%s", patched)
	}
	if !strings.Contains(string(patched), "- prefer tabs") {
		t.Fatalf("user content was lost:\n%s", patched)
	}

	if err := removeManagedBlock(path); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Errorf("round trip changed the file.\n got: %q\nwant: %q", restored, original)
	}
}

// TestManagedBlockKeepsFileMode: the target is the user's file, so a launch must not widen or narrow its permissions.
func TestManagedBlockKeepsFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".goosehints")
	if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedBlock(path, "x"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode changed to %v, want 0644", info.Mode().Perm())
	}
}

// TestManagedBlockRemovesAFileItCreated: a file that held nothing but our block did not exist before the launch, so leaving an empty one behind is still a change.
func TestManagedBlockRemovesAFileItCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".goosehints")
	if err := writeManagedBlock(path, "only ours"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("block file was not created: %v", err)
	}
	if err := removeManagedBlock(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file survived cleanup, want it gone")
	}
}

// TestManagedBlockConvergesAfterAMissedCleanup: a launch killed with SIGKILL never runs its defer, so the next one finds a stale block. It must converge on exactly one, not stack a second.
func TestManagedBlockConvergesAfterAMissedCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".goosehints")
	if err := writeManagedBlock(path, "first launch"); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedBlock(path, "second launch"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), managedBlockBegin); n != 1 {
		t.Errorf("found %d blocks, want exactly 1:\n%s", n, body)
	}
	if strings.Contains(string(body), "first launch") {
		t.Errorf("stale block survived:\n%s", body)
	}
	if !strings.Contains(string(body), "second launch") {
		t.Errorf("current block missing:\n%s", body)
	}
}

// TestManagedBlockRefusesToCreateAConfigDirectory: the parent directory belongs to the client. A machine where Goose has never stored global configuration must come out of a launch unchanged, not with a new ~/.config/goose.
func TestManagedBlockRefusesToCreateAConfigDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-existed", ".goosehints")
	if err := writeManagedBlock(missing, "x"); err != nil {
		t.Fatalf("a missing directory should be a quiet no-op, got: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Error("the client's configuration directory was conjured into existence")
	}
}

func TestGooseHintsPathRequiresAnExistingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	if got := gooseHintsPath(); got != "" {
		t.Errorf("gooseHintsPath() = %q with no goose directory, want empty", got)
	}
	if err := os.MkdirAll(filepath.Join(root, "goose"), 0o700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "goose", ".goosehints")
	if got := gooseHintsPath(); got != want {
		t.Errorf("gooseHintsPath() = %q, want %q", got, want)
	}
}

// TestGooseLaunchPatchesAndUnpatches walks the real path: the adapter's env function writes the block and records it, and TakeManagedBlockCleanup takes it back out.
func TestGooseLaunchPatchesAndUnpatches(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("TMUX", "")
	t.Setenv(TerminalModeEnvironment, "native")
	dir := filepath.Join(root, "goose")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	hints := filepath.Join(dir, ".goosehints")
	if err := os.WriteFile(hints, []byte("user hint\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool, err := Lookup("goose")
	if err != nil {
		t.Fatal(err)
	}
	env := tool.envFn("https://api.everyapi.ai", "sk-everyapi-test")
	body, err := os.ReadFile(hints)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "EveryAPI CLI") {
		t.Fatalf("goose hints did not receive the capability list:\n%s", body)
	}

	cleanup := TakeManagedBlockCleanup(env)
	if cleanup == nil {
		t.Fatal("no cleanup was registered for the patched file")
	}
	if _, ok := env[managedBlockMarker]; ok {
		t.Error("internal marker leaked into the child environment")
	}
	cleanup()
	restored, err := os.ReadFile(hints)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "user hint\n" {
		t.Errorf("cleanup did not restore the user's file: %q", restored)
	}
}
