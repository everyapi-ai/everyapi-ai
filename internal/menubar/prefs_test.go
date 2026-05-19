package menubar

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadPrefs_Missing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got := loadPrefs()
	if got != (preferences{}) {
		t.Errorf("loadPrefs (missing) = %+v, want zero", got)
	}
}

func TestLoadPrefs_HappyPath(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), "everyapi")
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(cfgDir))
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "menubar-prefs.json")
	body := `{
  "refresh_interval_seconds": 60,
  "sanitizer_listen": "127.0.0.1:9999",
  "mute_earnings_notifications": true,
  "mute_risk_notifications": false,
  "api_base": "https://api.dev.local"
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got := loadPrefs()
	want := preferences{
		RefreshIntervalSeconds: 60,
		SanitizerListen:        "127.0.0.1:9999",
		MuteEarnings:           true,
		MuteRisk:               false,
		APIBase:                "https://api.dev.local",
	}
	if got != want {
		t.Errorf("loadPrefs = %+v\nwant %+v", got, want)
	}
}

func TestEnsurePrefsFile_CreatesSeed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path, err := ensurePrefsFile()
	if err != nil {
		t.Fatalf("ensurePrefsFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("seed file empty")
	}

	// Idempotent — second call must not clobber existing content.
	if err := os.WriteFile(path, []byte(`{"refresh_interval_seconds": 120}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensurePrefsFile(); err != nil {
		t.Fatalf("second ensurePrefsFile: %v", err)
	}
	data2, _ := os.ReadFile(path)
	if string(data2) == string(data) {
		t.Error("ensurePrefsFile clobbered existing prefs (should be idempotent)")
	}
}

func TestResolveRefreshInterval(t *testing.T) {
	tests := []struct {
		name string
		prefs preferences
		want time.Duration
	}{
		{"zero falls back to default", preferences{}, refreshIntervalDefault},
		{"negative falls back", preferences{RefreshIntervalSeconds: -5}, refreshIntervalDefault},
		{"below floor clamps to floor", preferences{RefreshIntervalSeconds: 3}, refreshIntervalFloor},
		{"valid override honored", preferences{RefreshIntervalSeconds: 90}, 90 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Controller{prefs: tc.prefs}
			if got := c.resolveRefreshInterval(); got != tc.want {
				t.Errorf("resolveRefreshInterval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFallbackAPIBase(t *testing.T) {
	c := &Controller{prefs: preferences{APIBase: "https://api.local"}}
	if got := c.fallbackAPIBase(); got != "https://api.local" {
		t.Errorf("fallbackAPIBase (prefs set) = %q", got)
	}
	c.prefs = preferences{}
	if got := c.fallbackAPIBase(); got == "" {
		t.Error("fallbackAPIBase (prefs empty) returned empty")
	}
}
