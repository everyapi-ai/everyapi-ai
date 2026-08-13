package settings

import (
	"reflect"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// TestWriteKey covers the validation centralised in writeKey —
// the actual disk roundtrip is config's responsibility, tested
// there; we just make sure the dispatcher refuses garbage before
// the file is touched.
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

// TestLabelLanguage covers the "show what's actually active" path
// the list command uses when no explicit preference is stored.
func TestLabelLanguage(t *testing.T) {
	i18n.SetLanguage(i18n.LangEn)
	if got := labelLanguage(""); got != "(default: en)" {
		t.Errorf("empty pref: got %q, want '(default: en)'", got)
	}
	if got := labelLanguage("zh"); got != "zh" {
		t.Errorf("explicit pref: got %q, want zh", got)
	}
}

// TestMenuLayout_WriteReadRoundTrip covers the menu_layout key: valid
// values stick, invalid ones are rejected, and readKey reports the
// effective layout (empty → grouped).
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

// TestSettingsRowsCoverEveryKey pins the listing to the full key set.
// The two safety keys used to be settable but invisible: `everyapi use`
// asked for them once at launch and nothing surfaced the answer again,
// so a stale yes could only be undone by guessing the key name.
func TestSettingsRowsCoverEveryKey(t *testing.T) {
	want := []string{"language", "menu_layout", "gateway_region", "dangerous_mode", "codex_hook_trust_bypass"}
	s := &config.Settings{}
	rows := settingsRows(s)
	if len(rows) != len(want) {
		t.Fatalf("settingsRows has %d rows, want %d", len(rows), len(want))
	}
	for i, key := range want {
		if rows[i].key != key {
			t.Errorf("row %d = %q, want %q", i, rows[i].key, key)
		}
		if _, ok := readKey(s, rows[i].key); !ok {
			t.Errorf("row %d prints %q, which readKey rejects", i, rows[i].key)
		}
	}
}

// TestEverySettingsFieldIsListedOrHidden closes the half of the loop
// TestSettingsRowsCoverEveryKey leaves open: the rows only know what they
// were told, so a field added to config.Settings and wired into
// readKey/writeKey can still land settable-but-invisible — the exact
// defect this package just fixed. Every JSON field has to be either a
// listed key or a deliberate exception.
func TestEverySettingsFieldIsListedOrHidden(t *testing.T) {
	// tool_models is per-tool state `everyapi use` writes on the user's
	// behalf, not a preference anyone types; it has no `set` spelling.
	hidden := map[string]bool{"tool_models": true}
	listed := map[string]bool{}
	for _, row := range settingsRows(&config.Settings{}) {
		listed[row.key] = true
	}
	typ := reflect.TypeOf(config.Settings{})
	for i := range typ.NumField() {
		field := typ.Field(i)
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		if !listed[tag] && !hidden[tag] {
			t.Errorf("config.Settings.%s (json %q) is neither listed by settingsRows nor in the hidden allowlist", field.Name, tag)
		}
	}
}

// TestGatewayRegionRowsNameTheRealEndpoints: the row has to name the host
// the region actually dials, and that host comes from the SDK constants —
// a locale that spells it out instead would keep advertising the old one
// after a gateway move.
func TestGatewayRegionRowsNameTheRealEndpoints(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })
	for _, lang := range i18n.SupportedLanguages() {
		i18n.SetLanguage(lang)
		_, options := gatewayRegionRows()
		if !strings.Contains(options[1], config.DefaultAPIBase) {
			t.Errorf("%s: global row = %q, want it to name %s", lang, options[1], config.DefaultAPIBase)
		}
		if !strings.Contains(options[2], config.ChinaAPIBase) {
			t.Errorf("%s: cn row = %q, want it to name %s", lang, options[2], config.ChinaAPIBase)
		}
	}
}

// TestUsageDocumentsEveryKey guards the KEYS block in every locale: the
// help text listed only `language` while four other keys were live, which
// is what made them undiscoverable in the first place.
func TestUsageDocumentsEveryKey(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })
	rows := settingsRows(&config.Settings{})
	for _, lang := range i18n.SupportedLanguages() {
		i18n.SetLanguage(lang)
		usage := i18n.T("settings.usage")
		for _, row := range rows {
			if !strings.Contains(usage, row.key) {
				t.Errorf("%s: settings usage does not document key %q", lang, row.key)
			}
		}
	}
}

// TestEditorStepsMatchTheListing is the regression guard for the gap this
// package had: the editor asked two questions while five keys were live.
// Every listed key except language (handled by its own picker) needs a
// step, with the cursor on the stored value and a write-back that lands
// on the same field `settings set` would.
func TestEditorStepsMatchTheListing(t *testing.T) {
	i18n.SetLanguage(i18n.LangEn)
	s := &config.Settings{}
	steps := editorSteps(s)
	if want := len(settingsRows(s)) - 1; len(steps) != want {
		t.Fatalf("editor asks %d questions for %d listed keys", len(steps), want)
	}
	for i, step := range steps {
		if step.current < 0 || step.current >= len(step.options) {
			t.Errorf("step %d starts at row %d of %d options", i, step.current, len(step.options))
		}
	}

	steps[0].apply(1)
	steps[1].apply(2)
	steps[2].apply(1)
	steps[3].apply(2)
	if s.MenuLayout != "nested" {
		t.Errorf("MenuLayout = %q, want nested", s.MenuLayout)
	}
	if s.GatewayRegion != "cn" {
		t.Errorf("GatewayRegion = %q, want cn", s.GatewayRegion)
	}
	if got := labelOptionalBool(s.DangerousMode); got != "true" {
		t.Errorf("DangerousMode = %s, want true", got)
	}
	if got := labelOptionalBool(s.CodexHookTrustBypass); got != "false" {
		t.Errorf("CodexHookTrustBypass = %s, want false", got)
	}

	// Re-reading the same settings puts each cursor on what was stored.
	stored := editorSteps(s)
	for i, want := range []int{1, 2, 1, 2} {
		if stored[i].current != want {
			t.Errorf("step %d reopened at row %d, want %d", i, stored[i].current, want)
		}
	}

	// Row 0 of a tri-state clears the answer instead of storing false, so
	// `everyapi use` asks again rather than silently locking in "no".
	stored[2].apply(0)
	if s.DangerousMode != nil {
		t.Errorf("DangerousMode = %v, want nil after picking unset", *s.DangerousMode)
	}
}

// TestEditorKeepsGatewayRegionUnset is the regression guard for the
// editor quietly answering a question the user never touched: walking
// every step with Enter (i.e. applying the row the cursor opened on)
// must leave an empty gateway_region empty. `everyapi login` asks for
// the region once and only while the preference is empty
// (ensureGatewayRegionPreference), so pinning it to "global" here would
// silently retire that prompt for anyone who opened this editor to
// change, say, the language.
func TestEditorKeepsGatewayRegionUnset(t *testing.T) {
	i18n.SetLanguage(i18n.LangEn)
	s := &config.Settings{}
	for _, step := range editorSteps(s) {
		step.apply(step.current)
	}
	if s.GatewayRegion != "" {
		t.Errorf("GatewayRegion = %q after Enter-ing through the editor, want it left unset", s.GatewayRegion)
	}
	if s.DangerousMode != nil || s.CodexHookTrustBypass != nil {
		t.Errorf("safety keys answered by walking the editor: dangerous=%s hook_trust=%s",
			labelOptionalBool(s.DangerousMode), labelOptionalBool(s.CodexHookTrustBypass))
	}

	// The explicit rows still record a real choice, and reopening lands
	// on it rather than back on "not chosen".
	values, options := gatewayRegionRows()
	if len(values) != len(options) {
		t.Fatalf("%d region values but %d picker rows", len(values), len(options))
	}
	for i, want := range values {
		s.GatewayRegion = ""
		editorSteps(s)[1].apply(i)
		if s.GatewayRegion != want {
			t.Errorf("region row %d stored %q, want %q", i, s.GatewayRegion, want)
		}
		if got := gatewayRegionIndex(s.GatewayRegion); got != i {
			t.Errorf("region %q reopened at row %d, want %d", want, got, i)
		}
		// The list and the picker read the same row.
		if got := labelGatewayRegion(s.GatewayRegion); got != options[i] {
			t.Errorf("labelGatewayRegion(%q) = %q, want %q", want, got, options[i])
		}
	}
	// A hand-edited alias still lands on the cn row rather than off the
	// end of the table.
	if got := gatewayRegionIndex("china"); got != 2 {
		t.Errorf("gatewayRegionIndex(china) = %d, want 2", got)
	}
}

// TestSettingsKeyWidthCoversTheLongestKey pins the list column to the
// rows instead of a hand-counted constant, so a longer key added later
// still lines up.
func TestSettingsKeyWidthCoversTheLongestKey(t *testing.T) {
	rows := settingsRows(&config.Settings{})
	width := settingsKeyWidth(rows)
	for _, row := range rows {
		if len(row.key) > width {
			t.Errorf("key %q (%d chars) overflows the %d-char column", row.key, len(row.key), width)
		}
	}
	if extra := settingsKeyWidth(append(rows, settingRow{key: "a_much_longer_settings_key"})); extra <= width {
		t.Errorf("width %d did not grow for a longer key (was %d)", extra, width)
	}
}

// TestSafetyKeysAcceptUnset closes the get/set round trip: `get` and
// `list` both print "unset", so `set` has to take it back — otherwise
// the only way to clear an answer is the TTY editor, which a piped or
// CI session never reaches.
func TestSafetyKeysAcceptUnset(t *testing.T) {
	for _, key := range []string{"codex_hook_trust_bypass", "dangerous_mode"} {
		s := &config.Settings{}
		if err := writeKey(s, key, "true"); err != nil {
			t.Fatalf("writeKey(%s, true): %v", key, err)
		}
		if got, _ := readKey(s, key); got != "true" {
			t.Fatalf("readKey(%s) = %q, want true", key, got)
		}
		for _, spelling := range []string{"unset", "  UNSET "} {
			if err := writeKey(s, key, "true"); err != nil {
				t.Fatalf("writeKey(%s, true): %v", key, err)
			}
			if err := writeKey(s, key, spelling); err != nil {
				t.Fatalf("writeKey(%s, %q): %v", key, spelling, err)
			}
			if got, _ := readKey(s, key); got != "unset" {
				t.Errorf("readKey(%s) after set %q = %q, want unset", key, spelling, got)
			}
		}
		if err := writeKey(s, key, "maybe"); err == nil {
			t.Errorf("writeKey(%s, maybe) was accepted", key)
		}
	}
}

func TestTriStateRoundTrip(t *testing.T) {
	if v := triStateValue(0); v != nil {
		t.Errorf("triStateValue(0) = %v, want nil (not chosen yet)", *v)
	}
	if v := triStateValue(1); v == nil || !*v {
		t.Errorf("triStateValue(1) = %v, want true", v)
	}
	if v := triStateValue(2); v == nil || *v {
		t.Errorf("triStateValue(2) = %v, want false", v)
	}
	for _, want := range []*bool{nil, triStateValue(1), triStateValue(2)} {
		got := triStateValue(triStateIndex(want))
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("round trip changed %v into %v", want, got)
		}
	}
	// The list label and the picker row must come from one table, or the
	// editor would show a different wording than `settings list`.
	if got, want := labelTriState(nil), triStateOptions()[0]; got != want {
		t.Errorf("labelTriState(nil) = %q, want %q", got, want)
	}
}

// TestTriStateLabelsKeepTheLiteral: the label doubles as the argument
// hint for `settings set <key> <value>`, so every locale has to keep the
// unset/true/false literal in front of its translated explanation.
func TestTriStateLabelsKeepTheLiteral(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage(i18n.LangEn) })
	yes, no := triStateValue(1), triStateValue(2)
	for _, lang := range i18n.SupportedLanguages() {
		i18n.SetLanguage(lang)
		for _, c := range []struct {
			value   *bool
			literal string
		}{{nil, "unset"}, {yes, "true"}, {no, "false"}} {
			if got := labelTriState(c.value); !strings.HasPrefix(got, c.literal) {
				t.Errorf("%s: labelTriState(%v) = %q, want it to start with %q", lang, c.value, got, c.literal)
			}
		}
	}
}

func TestLanguageChoicesSelectsCurrent(t *testing.T) {
	langs, options, selected := languageChoices("zh")
	if len(langs) != len(options) {
		t.Fatalf("%d languages but %d picker rows", len(langs), len(options))
	}
	if langs[selected] != "zh" {
		t.Errorf("selected row = %q, want zh", langs[selected])
	}
	// No stored preference (or one whose locale was dropped) starts at the
	// first row rather than off the end of the slice.
	if _, _, sel := languageChoices(""); sel != 0 {
		t.Errorf("unset language selected row %d, want 0", sel)
	}
	if _, _, sel := languageChoices("klingon"); sel != 0 {
		t.Errorf("unknown language selected row %d, want 0", sel)
	}
	// Every shipped locale needs a native name, or its row degrades to
	// "zh-TW — zh-TW" and reads like a bug.
	for i, l := range langs {
		if options[i] == l+" — "+l {
			t.Errorf("%s has no native name; row reads %q", l, options[i])
		}
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
