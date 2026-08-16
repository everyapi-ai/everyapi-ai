package i18n

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

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
		{"zh-CN", LangZh},
		// Traditional-Chinese tags map to zh-TW, not Simplified zh.
		{"zh-Hant", LangZhTW},
		{"zh-TW", LangZhTW},
		{"zh-HK", LangZhTW},
		{"ZH_TW.UTF-8", LangZhTW},
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
		// Inject an en-only fixture key + assert zh fallback. The loader is init-only so we mutate the live map directly — safe because tests run single-threaded and we clean up.
		locales[LangEn]["__test.en_only"] = "hello"
		t.Cleanup(func() { delete(locales[LangEn], "__test.en_only") })

		SetLanguage(LangZh)
		if got := T("__test.en_only"); got != "hello" {
			t.Errorf("en fallback failed: %q", got)
		}
	})

	t.Run("unknown key returns the key itself", func(t *testing.T) {
		// Loud fallback so a developer notices the unregistered string in code review / on first run.
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

// TestSupportedLanguages confirms the loader discovers every locale file under embed and returns them sorted with en first. Sanity check that the embed actually happened — a build that ships zero locale files would silently return en for every T() call.
func TestSupportedLanguages(t *testing.T) {
	langs := SupportedLanguages()
	if len(langs) == 0 {
		t.Fatal("no languages loaded — locales/*.toml didn't embed?")
	}
	if langs[0] != LangEn {
		t.Errorf("first language = %q, want en (sort invariant)", langs[0])
	}
	// Every shipped locale must be reachable through the loader; a build that quietly dropped a {lang}.toml from the embed would otherwise show up only at runtime when a user picked the missing language.
	seen := map[string]bool{}
	for _, l := range langs {
		seen[l] = true
	}
	for _, want := range []string{LangEn, LangZh, LangZhTW, LangJa, LangKo, LangEs, LangDe, LangFr} {
		if !seen[want] {
			t.Errorf("missing %q in supported languages: %v", want, langs)
		}
	}
}

// localePlaceholderRe extracts printf verbs (%s %d %q %.2f %dh, plus explicit-index forms like %[2]s) from a format string.
var localePlaceholderRe = regexp.MustCompile(`%(\[\d+\])?[-#0-9.]*[a-zA-Z]`)

// localePlaceholderIndexRe strips the explicit %[N] argument index.
var localePlaceholderIndexRe = regexp.MustCompile(`\[\d+\]`)

// localePlaceholders returns the SORTED MULTISET of printf verbs in s, with explicit %[N] indices stripped. Both choices are deliberate: reordering args via %[2]s is a legal, README-recommended way to fit a language's word order, so the parity check compares which verbs appear (and how many), not their position — otherwise a correctly-reordered translation would spuriously fail. Literal %% is dropped first so it isn't mistaken for a verb.
func localePlaceholders(s string) []string {
	s = strings.ReplaceAll(s, "%%", "")
	raw := localePlaceholderRe.FindAllString(s, -1)
	out := make([]string, len(raw))
	for i, p := range raw {
		out[i] = localePlaceholderIndexRe.ReplaceAllString(p, "")
	}
	slices.Sort(out)
	return out
}

// TestLocaleParity is the dev-time guard against locale drift: every shipped locale must carry the SAME key set as en, with the same format placeholders (count + order) per key. Runtime still degrades gracefully — T() falls back to en for a missing key — but that fallback is exactly what let the secondary locales silently drift 54 keys behind before this test existed. Rule: add a key to en.toml → add it to every locale, with identical %-verbs in identical order (Go's printf is positional, so reordering verbs silently corrupts output unless done via explicit %[N] indices — which the placeholder check tolerates by comparing the index-stripped multiset).
//
// Do NOT add t.Parallel() here: TestT_Translation mutates the shared package-level `locales` map (it injects an en-only fixture key), and a parallel run could observe that key and report it missing from every other locale.
func TestLocaleParity(t *testing.T) {
	en, ok := locales[LangEn]
	if !ok {
		t.Fatal("en locale not loaded")
	}
	for lang, tbl := range locales {
		if lang == LangEn {
			continue
		}
		for k, ev := range en {
			tv, ok := tbl[k]
			if !ok {
				t.Errorf("%s: missing key %q (present in en)", lang, k)
				continue
			}
			if ep, tp := localePlaceholders(ev), localePlaceholders(tv); !slices.Equal(ep, tp) {
				t.Errorf("%s: key %q placeholder mismatch: en %v vs %v", lang, k, ep, tp)
			}
		}
		for k := range tbl {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: extra key %q (not in en)", lang, k)
			}
		}
	}
}

// TestInstallDiagnosticsAreNotNpmSpecific guards the two install-failure strings that any tool can reach. Resolution used to search only npm's global bin, so both messages named npm; they now also render for the curl|bash installers (gemini/claude/hermes), where telling the user to fix "npm's global bin directory" points at a directory that was never involved. Every locale has to stay neutral, not just en — these are the strings cmd/use actually prints, unlike the Go-side Error() text.
func TestInstallDiagnosticsAreNotNpmSpecific(t *testing.T) {
	keys := []string{"use.installed_not_on_path_dirs", "use.installer_missing"}
	for lang, tbl := range locales {
		for _, k := range keys {
			v, ok := tbl[k]
			if !ok {
				t.Errorf("%s: missing key %q", lang, k)
				continue
			}
			if strings.Contains(strings.ToLower(v), "npm") {
				t.Errorf("%s: %q still names npm; it renders for curl-installed tools too:\n  %s",
					lang, k, v)
			}
		}
	}
}

// TestLocaleMarkersBalanced ensures every locale value has an even number of ** emphasis markers. An unclosed marker would bold the rest of the string on a styled terminal, or leave a stray ** when piped. Presence of markers is NOT required (locales are marked incrementally), only balance.
func TestLocaleMarkersBalanced(t *testing.T) {
	for lang, tbl := range locales {
		for key, val := range tbl {
			if strings.Count(val, "**")%2 != 0 {
				t.Errorf("%s/%s has an odd number of ** markers: %q", lang, key, val)
			}
		}
	}
}

func TestLocalePlaceholders(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"%s contains %d", []string{"%d", "%s"}},
		// Explicit-index reordering must yield the same multiset as the un-indexed source — reordering args for grammar is legal and must not trip the parity check.
		{"%[2]s 含 %[1]d 個", []string{"%d", "%s"}},
		{"%d%% done", []string{"%d"}}, // literal %% is not a verb
		{"100%% complete", nil},
		{"%.2f / %5.1f%%", []string{"%.2f", "%5.1f"}},
		{"no verbs here", nil},
	}
	for _, c := range cases {
		if got := localePlaceholders(c.in); !slices.Equal(got, c.want) {
			t.Errorf("localePlaceholders(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
