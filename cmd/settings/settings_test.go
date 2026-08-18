package settings

import (
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// TestWriteKey covers the validation centralised in writeKey — the actual disk roundtrip is config's responsibility, tested there; we just make sure the dispatcher refuses garbage before the file is touched.
func TestWriteKey(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
		wantSet string // value of s.Language after the call (only checked when key=language and no err)
	}{
		{"language=en", "language", "en", false, "en"},
		{"language=zh", "language", "zh", false, "zh"},
		{"language case insensitive", "language", "ZH", false, "zh"},
		{"language with whitespace", "language", "  en  ", false, "en"},
		{"language unsupported", "language", "klingon", true, ""},
		{"gateway region global", "gateway_region", "global", false, ""},
		{"gateway region cn", "gateway_region", "cn", false, ""},
		{"gateway region china alias", "gateway_region", "china", false, ""},
		{"gateway region unsupported", "gateway_region", "mars", true, ""},
		{"unknown key", "color", "blue", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &config.Settings{}
			err := writeKey(s, c.key, c.value)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && c.key == "language" && s.Language != c.wantSet {
				t.Errorf("Language = %q, want %q", s.Language, c.wantSet)
			}
		})
	}
}

func TestReadKey(t *testing.T) {
	s := &config.Settings{Language: "zh"}
	if v, ok := readKey(s, "language"); !ok || v != "zh" {
		t.Errorf("readKey(language) = %q, %v", v, ok)
	}
	s.GatewayRegion = "cn"
	if v, ok := readKey(s, "gateway_region"); !ok || v != "cn" {
		t.Errorf("readKey(gateway_region) = %q, %v", v, ok)
	}
	if _, ok := readKey(s, "color"); ok {
		t.Errorf("readKey(color) should return ok=false")
	}
}

// TestLabelLanguage covers the "show what's actually active" path the list command uses when no explicit preference is stored.
func TestLabelLanguage(t *testing.T) {
	i18n.SetLanguage(i18n.LangEn)
	if got := labelLanguage(""); got != "(default: en)" {
		t.Errorf("empty pref: got %q, want '(default: en)'", got)
	}
	if got := labelLanguage("zh"); got != "zh" {
		t.Errorf("explicit pref: got %q, want zh", got)
	}
}

// TestMenuLayout_WriteReadRoundTrip covers the menu_layout key: valid values stick, invalid ones are rejected, and readKey reports the effective layout (empty → grouped).
func TestMenuLayout_WriteReadRoundTrip(t *testing.T) {
	s := &config.Settings{}

	// Empty stored value reads back as the effective default.
	if v, ok := readKey(s, "menu_layout"); !ok || v != "grouped" {
		t.Errorf("readKey(menu_layout) on empty = %q,%v; want \"grouped\",true", v, ok)
	}

	if err := writeKey(s, "menu_layout", "nested"); err != nil {
		t.Fatalf("writeKey nested: %v", err)
	}
	if s.MenuLayout != "nested" {
		t.Errorf("MenuLayout = %q, want nested", s.MenuLayout)
	}
	if v, _ := readKey(s, "menu_layout"); v != "nested" {
		t.Errorf("readKey(menu_layout) = %q, want nested", v)
	}

	// Case-insensitive + trims.
	if err := writeKey(s, "menu_layout", "  GROUPED "); err != nil {
		t.Fatalf("writeKey GROUPED: %v", err)
	}
	if s.MenuLayout != "grouped" {
		t.Errorf("MenuLayout = %q, want grouped", s.MenuLayout)
	}

	if err := writeKey(s, "menu_layout", "fancy"); err == nil {
		t.Error("writeKey accepted an invalid menu_layout value")
	}
}

func TestEffectiveMenuLayout(t *testing.T) {
	for in, want := range map[string]string{"": "grouped", "grouped": "grouped", "nested": "nested", "bogus": "grouped"} {
		if got := effectiveMenuLayout(in); got != want {
			t.Errorf("effectiveMenuLayout(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGatewayRegion_WriteReadRoundTrip(t *testing.T) {
	s := &config.Settings{}
	if v, ok := readKey(s, "gateway_region"); !ok || v != "global" {
		t.Errorf("readKey(gateway_region) on empty = %q,%v; want \"global\",true", v, ok)
	}
	if err := writeKey(s, "gateway_region", "  CN "); err != nil {
		t.Fatalf("writeKey cn: %v", err)
	}
	if s.GatewayRegion != "cn" {
		t.Errorf("GatewayRegion = %q, want cn", s.GatewayRegion)
	}
	if v, _ := readKey(s, "gateway_region"); v != "cn" {
		t.Errorf("readKey(gateway_region) = %q, want cn", v)
	}
	if err := writeKey(s, "gateway_region", "global"); err != nil {
		t.Fatalf("writeKey global: %v", err)
	}
	if s.GatewayRegion != "global" {
		t.Errorf("GatewayRegion = %q, want global", s.GatewayRegion)
	}
	if err := writeKey(s, "gateway_region", "eu"); err == nil {
		t.Error("writeKey accepted an invalid gateway_region value")
	}
}

func TestTerminalModeWriteReadRoundTrip(t *testing.T) {
	s := &config.Settings{}
	if value, ok := readKey(s, "terminal_mode"); !ok || value != "unset" {
		t.Fatalf("readKey(terminal_mode) on empty = %q,%v; want unset,true", value, ok)
	}
	if err := writeKey(s, "terminal_mode", "  TMUX "); err != nil {
		t.Fatalf("writeKey tmux: %v", err)
	}
	if s.TerminalMode != "tmux" {
		t.Fatalf("TerminalMode = %q, want tmux", s.TerminalMode)
	}
	if value, _ := readKey(s, "terminal_mode"); value != "tmux" {
		t.Fatalf("readKey(terminal_mode) = %q, want tmux", value)
	}
	if err := writeKey(s, "terminal_mode", "native"); err != nil {
		t.Fatalf("writeKey native: %v", err)
	}
	if err := writeKey(s, "terminal_mode", "screen"); err == nil {
		t.Fatal("writeKey accepted an invalid terminal_mode")
	}
}

func TestSafetyPreferencesWriteReadRoundTrip(t *testing.T) {
	s := &config.Settings{}
	for _, key := range []string{"codex_hook_trust_bypass", "dangerous_mode"} {
		if got, ok := readKey(s, key); !ok || got != "unset" {
			t.Fatalf("readKey(%s) unset = %q,%v; want unset,true", key, got, ok)
		}
		if err := writeKey(s, key, "true"); err != nil {
			t.Fatalf("writeKey(%s, true): %v", key, err)
		}
		if got, _ := readKey(s, key); got != "true" {
			t.Fatalf("readKey(%s) = %q, want true", key, got)
		}
		if err := writeKey(s, key, "false"); err != nil {
			t.Fatalf("writeKey(%s, false): %v", key, err)
		}
		if got, _ := readKey(s, key); got != "false" {
			t.Fatalf("readKey(%s) = %q, want false", key, got)
		}
		if err := writeKey(s, key, "maybe"); err == nil {
			t.Fatalf("writeKey(%s) accepted invalid boolean", key)
		}
	}
}

// The editor is the only surface most people ever see, and it used to ask two hard-coded questions — so gateway_region, codex_hook_trust_bypass and dangerous_mode existed in the file, in `settings set`, and in `settings list`, but were invisible and unreachable there. Tie the two together: every key writeKey accepts has to have a row.
func TestSettingRowsCoverEverySettingsKey(t *testing.T) {
	keys := []string{"language", "menu_layout", "gateway_region", "terminal_mode", "codex_hook_trust_bypass", "dangerous_mode"}
	rows := settingRows()
	rowKeys := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.key != "" {
			rowKeys[row.key] = true
		}
		if row.label == "" {
			t.Errorf("row %q has no label", row.key)
		}
		if row.value == nil || row.edit == nil {
			t.Errorf("row %q is missing a value renderer or editor", row.key)
		}
	}
	for _, key := range keys {
		if _, ok := readKey(&config.Settings{}, key); !ok {
			t.Fatalf("test is stale: %q is no longer a settings key", key)
		}
		if !rowKeys[key] {
			t.Errorf("settings key %q is missing from the interactive editor", key)
		}
	}
	if len(rowKeys) != len(keys) {
		t.Errorf("editor rows cover %d settings keys, want %d", len(rowKeys), len(keys))
	}
}

// The relay key is not a settings.json key, so it carries no key field — but it must still be offered, since it decides which models a launch can reach.
func TestSettingRowsOfferTheDefaultRelayKey(t *testing.T) {
	var found int
	for _, row := range settingRows() {
		if row.key == "" {
			found++
			if row.label != i18n.T("settings.default_key_label") {
				t.Errorf("non-settings row label = %q, want the default-key label", row.label)
			}
		}
	}
	if found != 1 {
		t.Fatalf("rows backed by something other than settings.json = %d, want exactly the relay key", found)
	}
}

func TestLabelDefaultRelayKey(t *testing.T) {
	t.Run("no credentials", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		if got := labelDefaultRelayKey(); got != i18n.T("settings.default_key_none") {
			t.Errorf("label = %q, want the not-set label", got)
		}
	})
	t.Run("cached key", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		creds := &config.Credentials{
			APIBase:         "https://api.example.test",
			AccessToken:     "tok",
			UserID:          1,
			RelayKey:        "sk-everyapi-x",
			RelayKeyTokenID: 812,
		}
		if err := config.Save(creds); err != nil {
			t.Fatal(err)
		}
		if got := labelDefaultRelayKey(); got != "#812" {
			t.Errorf("label = %q, want #812", got)
		}
	})
}

// Renders every row the way the menu does, so a row whose value function panics or returns an empty string fails here rather than on a user's screen.
func TestEditorMenuRendersEveryRow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	previous := i18n.Language()
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage(previous) })

	enabled := true
	s := &config.Settings{
		Language:      "zh",
		MenuLayout:    "nested",
		GatewayRegion: "cn",
		TerminalMode:  config.TerminalModeTmux,
		DangerousMode: &enabled,
	}
	var lines []string
	for _, row := range settingRows() {
		line := row.label + ": " + row.value(s)
		if row.value(s) == "" {
			t.Errorf("row %q rendered an empty value", row.label)
		}
		lines = append(lines, line)
	}
	lines = append(lines, i18n.T("settings.done"))
	if len(lines) != 8 {
		t.Fatalf("menu has %d lines, want 7 settings plus Done", len(lines))
	}
	// The value column has to show what is in effect, not the raw field: an unset tri-state reads "unset", not "false".
	if got := lines[2]; got != "Gateway region: cn" {
		t.Errorf("gateway region rendered as %q", got)
	}
	if got := lines[3]; got != "Terminal mode: tmux session" {
		t.Errorf("terminal mode rendered as %q", got)
	}
	if got := lines[4]; got != "Codex hook trust bypass: unset" {
		t.Errorf("unset tri-state rendered as %q", got)
	}
	if got := lines[5]; got != "Dangerous mode: on" {
		t.Errorf("set tri-state rendered as %q", got)
	}
	if got := lines[6]; got != "Default API key: not set" {
		t.Errorf("relay key row rendered as %q", got)
	}
	t.Log("interactive editor:\n  " + strings.Join(lines, "\n  "))
}

// Every row a person reads has to be a translated string. Three keys used to render their raw identifier as the label — and their value as a bare Go bool — so a zh session showed "dangerous_mode: true" in the middle of an otherwise translated screen.
func TestSettingRowLabelsAreTranslated(t *testing.T) {
	previous := i18n.Language()
	t.Cleanup(func() { i18n.SetLanguage(previous) })
	for _, lang := range []string{"en", "zh"} {
		i18n.SetLanguage(lang)
		for _, row := range settingRows() {
			if row.key != "" && row.label == row.key {
				t.Errorf("%s: row %q renders its raw settings key as the label", lang, row.key)
			}
			if strings.HasPrefix(row.label, "settings.") {
				t.Errorf("%s: row %q has an untranslated key as its label: %q", lang, row.key, row.label)
			}
		}
	}
}

// `settings get` / `set` are a scripting interface: those words are the ones the CLI accepts back, so they must NOT follow the display language.
func TestMachineBoolStaysEnglish(t *testing.T) {
	previous := i18n.Language()
	t.Cleanup(func() { i18n.SetLanguage(previous) })
	i18n.SetLanguage("zh")
	enabled := true
	cases := map[*bool]string{nil: "unset", &enabled: "true"}
	for value, want := range cases {
		if got := labelOptionalBool(value); got != want {
			t.Errorf("labelOptionalBool = %q, want %q even under a non-English locale", got, want)
		}
	}
	if got := displayOptionalBool(&enabled); got == "true" {
		t.Error("display form should be localized, got the machine word")
	}
}
