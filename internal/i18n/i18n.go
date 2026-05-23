// Package i18n is the CLI's translation layer.
//
// Most user-visible text in `everyapi` comes from the backend API
// (every error message in cmd/* surfaces a server `message` field).
// Backend already speaks zh / en via its own i18n package — we just
// have to send the Accept-Language header so the backend translates
// for us. The CLI side here only owns CLI-originated strings:
// launcher headers, picker prompts, no-creds errors, USAGE blocks,
// and the like.
//
// Translations live in TOML files under ./locales — one per
// language. `//go:embed` bundles them into the binary so there's no
// runtime file I/O. Add a language by dropping a new {lang}.toml
// here; the loader picks it up at init() and the lang shows up in
// SupportedLanguages() automatically. No cmd-file edits needed.
//
// Lookup is by dotted key: a TOML table [token] containing
// no_tokens = "..." becomes the i18n.T("token.no_tokens") key.
// Nested tables ([token.label]) flatten the same way:
// i18n.T("token.label.name").
//
// Missing-key behaviour (loud-fail by design):
//   - key exists in current lang        → that translation
//   - key missing in current, en has it → en value (degradation)
//   - key absent everywhere             → the key itself (developer
//     sees it in code review or
//     first manual smoke)
package i18n

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

//go:embed locales/*.toml
var localesFS embed.FS

const (
	// LangEn / LangZh / LangJa / LangKo / LangEs / LangDe / LangFr are
	// the languages currently shipped. Adding more later means dropping
	// {lang}.toml under ./locales (loader picks them up at init) plus a
	// matching const here only if Go code needs to reference the new
	// tag by name. The loader doesn't require it.
	LangEn   = "en"
	LangZh   = "zh"
	LangZhTW = "zh-TW"
	LangJa   = "ja"
	LangKo   = "ko"
	LangEs   = "es"
	LangDe   = "de"
	LangFr   = "fr"
)

// locales is the loaded table: lang → flat dotted-key → string.
// Populated once at init() from the embedded *.toml files; never
// mutated thereafter, so T() reads it lock-free.
var locales = map[string]map[string]string{}

// supportedLangs is the alphabetically-sorted list of language
// codes discovered at init() by scanning ./locales for *.toml. The
// public SupportedLanguages() returns a copy so callers can't
// mutate the underlying slice.
var supportedLangs []string

// SupportedLanguages returns the sorted language codes the binary
// was built with. Always includes "en" first if present, then the
// rest alphabetically.
func SupportedLanguages() []string {
	out := make([]string, len(supportedLangs))
	copy(out, supportedLangs)
	return out
}

// SetLanguage records the active language for the duration of the
// process. SetLanguage("") falls back to en. Mutex-guarded so a
// test (or a future runtime-switch helper) can change it without
// data-race fireworks against in-flight goroutines.
var (
	mu      sync.RWMutex
	current = LangEn
)

func SetLanguage(lang string) {
	lang = normalize(lang)
	if lang == "" {
		lang = LangEn
	}
	mu.Lock()
	current = lang
	mu.Unlock()
}

// Language returns whatever SetLanguage most recently set.
func Language() string {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// T returns the translation of key in the current language. Falls
// back to en if zh (or any non-en lang) is missing the key, then
// to the key itself if en is also missing it.
func T(key string) string {
	mu.RLock()
	lang := current
	mu.RUnlock()
	if tbl, ok := locales[lang]; ok {
		if v, ok := tbl[key]; ok {
			return v
		}
	}
	if lang != LangEn {
		if tbl, ok := locales[LangEn]; ok {
			if v, ok := tbl[key]; ok {
				return v
			}
		}
	}
	return key
}

// DetectFromEnv consults LC_ALL / LANG / LC_MESSAGES the same way
// libc does and returns one of the SupportedLanguages, defaulting
// to en. Called by main() when settings.json doesn't carry an
// explicit preference. Honours EVERYAPI_LANG first so a user can
// force a language via shell env without touching the file.
func DetectFromEnv() string {
	for _, k := range []string{"EVERYAPI_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if lang := normalize(v); lang != "" {
				return lang
			}
		}
	}
	return LangEn
}

// normalize collapses an IETF tag (en_US.UTF-8, zh_CN, zh-Hant,
// ja_JP.UTF-8, ko_KR, es_MX, de_DE, fr_FR) to the bare language
// subtag we support. Traditional-Chinese tags (zh-TW / zh-Hant /
// zh-HK / zh-MO) map to zh-TW; every other zh* falls back to
// Simplified zh. Anything we don't recognise returns "" so the
// caller can apply its own default.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// libc locale tags use "_" (zh_TW.UTF-8); IETF tags use "-"
	// (zh-TW). Fold "_" to "-" so both forms hit the same prefix —
	// otherwise zh_TW from $LANG would slip past the zh-tw case and
	// be mis-detected as Simplified.
	s = strings.ReplaceAll(s, "_", "-")
	switch {
	// These Traditional cases MUST stay above the bare "zh" case below:
	// "zh-tw" etc. also have prefix "zh", so the switch's first-match
	// order is load-bearing.
	case strings.HasPrefix(s, "zh-tw"), strings.HasPrefix(s, "zh-hant"), strings.HasPrefix(s, "zh-hk"), strings.HasPrefix(s, "zh-mo"):
		return LangZhTW
	case strings.HasPrefix(s, "zh"):
		return LangZh
	case strings.HasPrefix(s, "en"):
		return LangEn
	case strings.HasPrefix(s, "ja"):
		return LangJa
	case strings.HasPrefix(s, "ko"):
		return LangKo
	case strings.HasPrefix(s, "es"):
		return LangEs
	case strings.HasPrefix(s, "de"):
		return LangDe
	case strings.HasPrefix(s, "fr"):
		return LangFr
	}
	return ""
}

func init() {
	entries, err := fs.ReadDir(localesFS, "locales")
	if err != nil {
		panic(fmt.Errorf("i18n: read embed dir: %w", err))
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		lang := strings.TrimSuffix(e.Name(), ".toml")
		data, err := fs.ReadFile(localesFS, "locales/"+e.Name())
		if err != nil {
			panic(fmt.Errorf("i18n: read %s: %w", e.Name(), err))
		}
		raw := map[string]any{}
		if err := toml.Unmarshal(data, &raw); err != nil {
			panic(fmt.Errorf("i18n: parse %s: %w", e.Name(), err))
		}
		flat := map[string]string{}
		flatten("", raw, flat)
		locales[lang] = flat
	}
	for lang := range locales {
		supportedLangs = append(supportedLangs, lang)
	}
	// en first (when present) so picker UIs / log output have a
	// stable, predictable first entry; rest alphabetical.
	sort.Slice(supportedLangs, func(i, j int) bool {
		if supportedLangs[i] == LangEn {
			return true
		}
		if supportedLangs[j] == LangEn {
			return false
		}
		return supportedLangs[i] < supportedLangs[j]
	})
}

// flatten walks a nested TOML map ([section.subsection]) and emits
// dotted-key entries into out. String values are kept as-is;
// non-string leaves (int / bool / array / inline-table) are
// silently skipped — translations are always strings, so a number
// or bool slipping in is a TOML mistake. T() will fall back to
// the bare key for the un-loaded entry, which is visible during a
// smoke test of the affected command. Choose silent-skip over
// panic because one bad locale shouldn't crash the binary at
// startup; choose silent-skip over warning because there's no
// reasonable log destination available during package init.
func flatten(prefix string, src map[string]any, out map[string]string) {
	for k, v := range src {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case string:
			out[key] = val
		case map[string]any:
			flatten(key, val, out)
		}
	}
}
