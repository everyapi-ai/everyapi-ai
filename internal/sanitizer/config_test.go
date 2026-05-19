package sanitizer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempConfigDir points XDG_CONFIG_HOME at a t.TempDir() for the
// duration of the test, so LoadFileConfig / SaveFileConfig don't
// touch the real user home. Returns the resolved sanitizer.json path
// for direct assertions.
func withTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "everyapi", "sanitizer.json")
}

func TestLoadFileConfig_MissingFile(t *testing.T) {
	withTempConfigDir(t)
	fc, err := LoadFileConfig()
	if err != nil {
		t.Fatalf("missing file should yield empty config + nil err, got err=%v", err)
	}
	if len(fc.Disabled) != 0 || len(fc.CustomPatterns) != 0 {
		t.Errorf("missing file should yield empty config, got %+v", fc)
	}
}

func TestSaveLoadFileConfig_RoundTrip(t *testing.T) {
	withTempConfigDir(t)
	in := &FileConfig{
		Disabled: []string{DetectorLuhnCreditCard},
		CustomPatterns: []UserPattern{
			{Name: "internal-id", Regex: `INT-\d+`},
		},
	}
	if err := SaveFileConfig(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := LoadFileConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(out.Disabled) != 1 || out.Disabled[0] != DetectorLuhnCreditCard {
		t.Errorf("disabled list round-trip failed: %+v", out.Disabled)
	}
	if len(out.CustomPatterns) != 1 || out.CustomPatterns[0].Regex != `INT-\d+` {
		t.Errorf("custom patterns round-trip failed: %+v", out.CustomPatterns)
	}
}

func TestSaveFileConfig_FilePermsAndAtomicity(t *testing.T) {
	path := withTempConfigDir(t)
	if err := SaveFileConfig(&FileConfig{}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
	// .tmp leftover must not exist after a clean save.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp leftover exists after rename")
	}
}

func TestBuildDetectors_NilReceiver(t *testing.T) {
	var fc *FileConfig
	got := fc.BuildDetectors()
	if len(got) != len(BuiltinDetectors()) {
		t.Errorf("nil receiver should yield all built-ins, got %d", len(got))
	}
}

func TestBuildDetectors_DisableFilters(t *testing.T) {
	fc := &FileConfig{Disabled: []string{DetectorChineseID, DetectorLuhnCreditCard}}
	got := fc.BuildDetectors()
	for _, d := range got {
		if d.Name() == DetectorChineseID || d.Name() == DetectorLuhnCreditCard {
			t.Errorf("disabled detector %q still active", d.Name())
		}
	}
	// Built-in count minus 2 — plus 0 custom.
	if len(got) != len(BuiltinDetectors())-2 {
		t.Errorf("len after disable = %d, want %d", len(got), len(BuiltinDetectors())-2)
	}
}

func TestBuildDetectors_CustomAppended(t *testing.T) {
	fc := &FileConfig{
		CustomPatterns: []UserPattern{
			{Name: "test-id", Regex: `TID-\d{4}`},
		},
	}
	got := fc.BuildDetectors()
	if len(got) != len(BuiltinDetectors())+1 {
		t.Errorf("len with custom = %d, want %d", len(got), len(BuiltinDetectors())+1)
	}
	last := got[len(got)-1]
	if !strings.Contains(last.Name(), "test-id") {
		t.Errorf("custom detector not last / wrong name: %q", last.Name())
	}
}

func TestBuildDetectors_UnknownDisabledIgnored(t *testing.T) {
	// Future-compat: a disabled name no current binary knows about
	// is silently ignored — doesn't crash, doesn't skip an unrelated
	// detector.
	fc := &FileConfig{Disabled: []string{"some_unknown_future_detector"}}
	got := fc.BuildDetectors()
	if len(got) != len(BuiltinDetectors()) {
		t.Errorf("unknown disabled name shouldn't reduce active set: got %d", len(got))
	}
}

func TestAllBuiltinNames(t *testing.T) {
	names := AllBuiltinNames()
	if len(names) != len(BuiltinDetectors()) {
		t.Errorf("AllBuiltinNames len = %d, want %d", len(names), len(BuiltinDetectors()))
	}
	for _, n := range names {
		if n == "" {
			t.Errorf("empty detector name in list: %v", names)
		}
	}
}
