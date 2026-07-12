package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallRejectsExtraArgsBeforePlanning(t *testing.T) {
	if err := Uninstall([]string{"--dry-run", "typo"}); err == nil {
		t.Fatal("uninstall accepted an extra positional")
	}
}

// withIsolatedHome points HOME / XDG_CONFIG_HOME / XDG_DATA_HOME at a
// tempdir for the duration of the test, returns the resolved
// configDir + dataDir so callers can seed them and assert later. The
// XDG vars override the HOME fallback in config.ConfigDir() and
// uninstallDataDir(), so setting both gives us full control regardless
// of how the helpers resolve paths.
func withIsolatedHome(t *testing.T) (configDir, dataDir string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	return filepath.Join(tmp, "config", "everyapi"),
		filepath.Join(tmp, "data", "everyapi")
}

// seedTree creates dir + a single marker file inside, so subsequent
// removal assertions have something concrete to compare against (an
// empty dir is technically a successful create; the marker proves the
// post-uninstall absence isn't a false positive from a no-op).
func seedTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("seed"), 0o600); err != nil {
		t.Fatalf("write marker in %s: %v", dir, err)
	}
}

// stubBinaryRemover swaps binaryRemover for a recorder so the test
// process's own executable can never be unlinked, even on an
// accidentally-permissive test run. Records calls so the binary-removal
// path is still observable.
func stubBinaryRemover(t *testing.T) *[]string {
	t.Helper()
	orig := binaryRemover
	var calls []string
	binaryRemover = func(p string) error {
		calls = append(calls, p)
		return nil
	}
	t.Cleanup(func() { binaryRemover = orig })
	return &calls
}

func TestUninstall_RemovesConfigAndData(t *testing.T) {
	configDir, dataDir := withIsolatedHome(t)
	seedTree(t, configDir)
	seedTree(t, dataDir)
	_ = stubBinaryRemover(t)

	// --keep-binary keeps the assertion focused on config/data wiping;
	// the binary-removal hook is exercised by the dedicated subtest
	// below to avoid coupling these two checks.
	if err := Uninstall([]string{"--yes", "--keep-binary"}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("configDir still present: stat err = %v", err)
	}
	if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dataDir still present: stat err = %v", err)
	}
}

func TestUninstall_DryRunChangesNothing(t *testing.T) {
	configDir, dataDir := withIsolatedHome(t)
	seedTree(t, configDir)
	seedTree(t, dataDir)
	calls := stubBinaryRemover(t)

	if err := Uninstall([]string{"--dry-run"}); err != nil {
		t.Fatalf("Uninstall --dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "marker")); err != nil {
		t.Errorf("dry-run removed configDir/marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "marker")); err != nil {
		t.Errorf("dry-run removed dataDir/marker: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("dry-run invoked binaryRemover: %v", *calls)
	}
}

func TestUninstall_KeepConfigPreservesConfigDir(t *testing.T) {
	configDir, dataDir := withIsolatedHome(t)
	seedTree(t, configDir)
	seedTree(t, dataDir)
	_ = stubBinaryRemover(t)

	if err := Uninstall([]string{"--yes", "--keep-config", "--keep-binary"}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "marker")); err != nil {
		t.Errorf("--keep-config removed configDir: %v", err)
	}
	if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--keep-config should still wipe dataDir: stat err = %v", err)
	}
}

func TestUninstall_KeepDataPreservesDataDir(t *testing.T) {
	configDir, dataDir := withIsolatedHome(t)
	seedTree(t, configDir)
	seedTree(t, dataDir)
	_ = stubBinaryRemover(t)

	if err := Uninstall([]string{"--yes", "--keep-data", "--keep-binary"}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--keep-data should still wipe configDir: stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "marker")); err != nil {
		t.Errorf("--keep-data removed dataDir: %v", err)
	}
}

func TestUninstall_BinaryRemovalHookFires(t *testing.T) {
	configDir, dataDir := withIsolatedHome(t)
	seedTree(t, configDir)
	seedTree(t, dataDir)
	calls := stubBinaryRemover(t)

	// No --keep-binary this time. The stubbed remover records the path
	// instead of actually unlinking, so the test process survives.
	if err := Uninstall([]string{"--yes"}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	// Detection might land on brew (we run under Cellar/) in unusual
	// CI setups, in which case the hook is intentionally skipped — the
	// brew branch prints a hint instead. Assert one OR the other so
	// the test doesn't go yellow on a tester's brew-installed Go.
	method := detectInstallMethod()
	switch method {
	case installMethodBrew:
		if len(*calls) != 0 {
			t.Errorf("brew install method should not invoke binaryRemover, got %v", *calls)
		}
	default:
		// Two calls: the binary itself, then the best-effort cleanup of
		// the .old sibling install.ps1's upgrade-over-a-running-binary
		// dance can leave behind.
		if len(*calls) != 2 {
			t.Fatalf("expected binaryRemover calls for the binary and its .old for %s install, got %v", method, *calls)
		}
		// The path is whatever os.Executable() returned for the test
		// binary — we don't pin its exact value (varies per test
		// runner), only that the hook was invoked with a non-empty
		// argument so the production path obviously gets called.
		if (*calls)[0] == "" {
			t.Error("binaryRemover called with empty path")
		}
		if (*calls)[1] != (*calls)[0]+".old" {
			t.Errorf("second binaryRemover call = %q, want %q", (*calls)[1], (*calls)[0]+".old")
		}
	}
}

// TestUninstall_MissingDirsAreNoOps confirms that running uninstall on
// a clean machine (no config dir, no data dir, no creds) succeeds
// silently — important because users may run this defensively to "make
// sure nothing's left" without actually having an install.
func TestUninstall_MissingDirsAreNoOps(t *testing.T) {
	configDir, dataDir := withIsolatedHome(t)
	_ = stubBinaryRemover(t)
	// Deliberately do NOT seed configDir / dataDir — they don't exist.

	if err := Uninstall([]string{"--yes", "--keep-binary"}); err != nil {
		t.Fatalf("Uninstall on clean machine: %v", err)
	}
	// Sanity: nothing was conjured into existence.
	if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("configDir created unexpectedly: %v", err)
	}
	if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dataDir created unexpectedly: %v", err)
	}
}
