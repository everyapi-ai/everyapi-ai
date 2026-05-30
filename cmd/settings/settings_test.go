package settings

import (
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
