package i18n

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// IETF tags collapse to bare language subtags.
		{"en", LangEn},
		{"en_US", LangEn},
		{"en_US.UTF-8", LangEn},
		{"en-GB", LangEn},
		{"zh", LangZh},
		{"zh_CN", LangZh},
		{"zh-Hant", LangZh},
		{"ZH_TW.UTF-8", LangZh},
		{"ja", LangJa},
		{"ja_JP.UTF-8", LangJa},
		{"ko", LangKo},
		{"ko_KR", LangKo},
		{"es", LangEs},
		{"es_MX.UTF-8", LangEs},
		{"de", LangDe},
		{"de_DE", LangDe},
		{"fr", LangFr},
		{"fr_FR.UTF-8", LangFr},

		// Unrecognised → "" (caller's default kicks in).
		{"", ""},
		{"   ", ""},
		{"klingon", ""},
	}
	for _, c := range cases {
		if got := normalize(c.in); got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestT_Translation(t *testing.T) {
	t.Run("zh translation wins when set", func(t *testing.T) {
		SetLanguage(LangZh)
		got := T("settings.saved")
		if got != "设置已保存。" {
			t.Errorf("zh fallthrough failed: %q", got)
		}
	})

	t.Run("en is used when set", func(t *testing.T) {
		SetLanguage(LangEn)
		if T("settings.saved") != "Settings saved." {
			t.Errorf("en lookup failed")
		}
	})

	t.Run("missing zh falls back to en", func(t *testing.T) {
		// Inject an en-only fixture key + assert zh fallback. The
		// loader is init-only so we mutate the live map directly —
		// safe because tests run single-threaded and we clean up.
		locales[LangEn]["__test.en_only"] = "hello"
		t.Cleanup(func() { delete(locales[LangEn], "__test.en_only") })

		SetLanguage(LangZh)
		if got := T("__test.en_only"); got != "hello" {
			t.Errorf("en fallback failed: %q", got)
		}
	})

	t.Run("unknown key returns the key itself", func(t *testing.T) {
		// Loud fallback so a developer notices the unregistered
		// string in code review / on first run.
		if got := T("definitely.not.a.real.key"); got != "definitely.not.a.real.key" {
			t.Errorf("unknown key fallback returned %q", got)
		}
	})
}

func TestDetectFromEnv(t *testing.T) {
	for _, k := range []string{"EVERYAPI_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		t.Setenv(k, "")
	}

	t.Run("default to en when nothing set", func(t *testing.T) {
		for _, k := range []string{"EVERYAPI_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
			t.Setenv(k, "")
		}
		if got := DetectFromEnv(); got != LangEn {
			t.Errorf("got %q, want en", got)
		}
	})

	t.Run("LANG=zh_CN.UTF-8 detected as zh", func(t *testing.T) {
		t.Setenv("LANG", "zh_CN.UTF-8")
		if got := DetectFromEnv(); got != LangZh {
			t.Errorf("got %q, want zh", got)
		}
	})

	t.Run("EVERYAPI_LANG overrides LANG", func(t *testing.T) {
		t.Setenv("LANG", "zh_CN.UTF-8")
		t.Setenv("EVERYAPI_LANG", "en")
		if got := DetectFromEnv(); got != LangEn {
			t.Errorf("got %q, want en (env override)", got)
		}
	})

	t.Run("unrecognised LANG falls through to en", func(t *testing.T) {
		t.Setenv("LANG", "klingon")
		if got := DetectFromEnv(); got != LangEn {
			t.Errorf("got %q, want en", got)
		}
	})
}

// TestSupportedLanguages confirms the loader discovers every locale
// file under embed and returns them sorted with en first. Sanity
// check that the embed actually happened — a build that ships zero
// locale files would silently return en for every T() call.
func TestSupportedLanguages(t *testing.T) {
	langs := SupportedLanguages()
	if len(langs) == 0 {
		t.Fatal("no languages loaded — locales/*.toml didn't embed?")
	}
	if langs[0] != LangEn {
		t.Errorf("first language = %q, want en (sort invariant)", langs[0])
	}
	// Every shipped locale must be reachable through the loader; a
	// build that quietly dropped a {lang}.toml from the embed would
	// otherwise show up only at runtime when a user picked the
	// missing language.
	seen := map[string]bool{}
	for _, l := range langs {
		seen[l] = true
	}
	for _, want := range []string{LangEn, LangZh, LangJa, LangKo, LangEs, LangDe, LangFr} {
		if !seen[want] {
			t.Errorf("missing %q in supported languages: %v", want, langs)
		}
	}
}
