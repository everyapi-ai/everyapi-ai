// Package settings wires `everyapi settings …` — the CLI's
// preference surface, persisted alongside credentials in
// ConfigDir. Settings are intentionally non-secret: language,
// launcher layout, gateway region, and future CLI preferences.
//
// File shape: clients/sdk/config/settings.go owns the Settings
// struct + load/save. This package is the human-facing dispatcher.
package settings

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) == 0 {
		// Bare 'everyapi settings' on a TTY → interactive editor.
		// On a pipe/script → fall through to list so it's still
		// useful as a status query.
		if cliprompt.IsInteractive() {
			return runInteractive()
		}
		return runList(nil)
	}
	switch args[0] {
	case "help", "--help", "-h":
		cliout.Println(i18n.T("settings.usage"))
		return nil
	case "list":
		return runList(args[1:])
	case "get":
		return runGet(args[1:])
	case "set":
		return runSet(args[1:])
	case "reset":
		return runReset(args[1:])
	default:
		cliout.Println(i18n.T("settings.usage"))
		return fmt.Errorf(i18n.T("common.unknown_subcommand"), "settings", args[0])
	}
}

// --- list / get -----------------------------------------------------

func runList(args []string) error {
	fs := flag.NewFlagSet("settings list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := config.LoadSettings()
	if err != nil {
		return err
	}
	cliout.Printf("%s\n", i18n.T("settings.current"))
	cliout.Printf("  %s: %s\n", i18n.T("settings.lang_label"), labelLanguage(s.Language))
	cliout.Printf("  %s: %s\n", i18n.T("settings.menu_label"), labelMenuLayout(s.MenuLayout))
	cliout.Printf("  %s: %s\n", "gateway_region", labelGatewayRegion(s.GatewayRegion))
	path, _ := config.SettingsPath()
	if path != "" {
		cliout.Printf("\n%s %s\n", i18n.T("settings.file_at"), path)
	}
	return nil
}

func runGet(args []string) error {
	if len(args) == 0 {
		return errors.New(i18n.T("settings.usage_get"))
	}
	s, err := config.LoadSettings()
	if err != nil {
		return err
	}
	val, ok := readKey(s, args[0])
	if !ok {
		return fmt.Errorf(i18n.T("settings.unknown_key"), args[0])
	}
	cliout.Println(val)
	return nil
}

// --- set ------------------------------------------------------------

func runSet(args []string) error {
	if len(args) < 2 {
		return errors.New(i18n.T("settings.usage_set"))
	}
	key, value := args[0], args[1]
	s, err := config.LoadSettings()
	if err != nil {
		return err
	}
	if err := writeKey(s, key, value); err != nil {
		return err
	}
	if err := config.SaveSettings(s); err != nil {
		return err
	}
	cliout.Println(i18n.T("settings.saved"))
	// Apply immediately so the rest of the process (and the next
	// invocation alike) speaks the new language.
	if key == "language" {
		i18n.SetLanguage(value)
		// Export the resolved canonical tag (SetLanguage normalizes it),
		// not the raw value — the SDK forwards EVERYAPI_LANG verbatim as
		// Accept-Language, so the wire header must match what we resolved.
		_ = os.Setenv("EVERYAPI_LANG", i18n.Language())
	}
	return nil
}

// --- reset ----------------------------------------------------------

func runReset(args []string) error {
	fs := flag.NewFlagSet("settings reset", flag.ContinueOnError)
	yes := fs.Bool("y", false, "skip confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*yes && cliprompt.IsInteractive() {
		ok, err := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			i18n.T("settings.reset_confirm"),
			false,
		)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println(i18n.T("common.canceled"))
			return nil
		}
	}
	if err := config.SaveSettings(&config.Settings{}); err != nil {
		return err
	}
	i18n.SetLanguage(i18n.LangEn)
	_ = os.Unsetenv("EVERYAPI_LANG")
	cliout.Println(i18n.T("settings.saved"))
	return nil
}

// --- interactive ----------------------------------------------------

func runInteractive() error {
	s, err := config.LoadSettings()
	if err != nil {
		return err
	}
	// Build options from the live SupportedLanguages list (sorted en
	// first by the loader) so a dropped-in {lang}.toml shows up
	// automatically. Each row is "<code> — <native name>" — the
	// native name comes from a small lookup table because we'd
	// otherwise need a "language.native_name" key in every locale
	// just for self-labelling.
	langs := i18n.SupportedLanguages()
	nativeName := map[string]string{
		"en": "English",
		"zh": "中文",
		"ja": "日本語",
		"ko": "한국어",
		"es": "Español",
		"de": "Deutsch",
		"fr": "Français",
	}
	options := make([]string, len(langs))
	cur := 0
	for i, l := range langs {
		name := nativeName[l]
		if name == "" {
			name = l
		}
		options[i] = fmt.Sprintf("%s — %s", l, name)
		if l == s.Language {
			cur = i
		}
	}
	idx, err := cliprompt.PickWithSelected(i18n.T("settings.lang_label"), options, cur)
	if err != nil {
		// Esc / Ctrl-C from the picker — treat as "no change" so
		// the user can bail without writing the file.
		if errors.Is(err, cliprompt.ErrPickCancelled) {
			cliout.Println(i18n.T("common.canceled"))
			return nil
		}
		return err
	}
	picked := langs[idx]
	s.Language = picked
	// Apply the language immediately so the menu-layout picker below
	// renders its labels in the just-chosen language.
	i18n.SetLanguage(picked)
	// Export the resolved canonical tag (normalized), not the raw pick.
	_ = os.Setenv("EVERYAPI_LANG", i18n.Language())

	// Second question: launcher menu layout (grouped single screen vs.
	// nested category picker). Esc here keeps the language choice and
	// leaves the layout unchanged.
	layouts := []string{"grouped", "nested"}
	layoutOpts := []string{i18n.T("settings.menu_grouped"), i18n.T("settings.menu_nested")}
	curLayout := 0
	if effectiveMenuLayout(s.MenuLayout) == "nested" {
		curLayout = 1
	}
	lidx, lerr := cliprompt.PickWithSelected(i18n.T("settings.menu_label"), layoutOpts, curLayout)
	if lerr != nil {
		if !errors.Is(lerr, cliprompt.ErrPickCancelled) {
			return lerr
		}
		// Esc: skip the layout change, still persist the language.
	} else {
		s.MenuLayout = layouts[lidx]
	}

	if err := config.SaveSettings(s); err != nil {
		return err
	}
	cliout.Println(i18n.T("settings.saved"))
	return nil
}

// --- key plumbing ---------------------------------------------------

// readKey + writeKey centralise the "key string ↔ struct field"
// mapping. Today there's one key; the dispatcher style still pays
// off as a stable surface for `settings get` to enumerate when a
// second key lands.
func readKey(s *config.Settings, key string) (string, bool) {
	switch key {
	case "language":
		return s.Language, true
	case "menu_layout":
		return effectiveMenuLayout(s.MenuLayout), true
	case "gateway_region":
		return config.EffectiveGatewayRegion(s.GatewayRegion), true
	}
	return "", false
}

func writeKey(s *config.Settings, key, value string) error {
	switch key {
	case "language":
		// Match case-insensitively and persist the canonical tag the locale
		// list advertises (e.g. "zh-TW"), so `settings set language zh-TW` —
		// or any case variant — is accepted and `settings get` echoes the
		// canonical form. The old lowercase-then-exact compare wrongly
		// rejected the only mixed-case tag, zh-TW.
		v := strings.TrimSpace(value)
		supported := i18n.SupportedLanguages()
		for _, sup := range supported {
			if strings.EqualFold(sup, v) {
				s.Language = sup
				return nil
			}
		}
		// Build the supported-list dynamically so the message stays
		// truthful when a locale is added or removed without anyone
		// remembering to retranslate the static lang_invalid copy.
		return fmt.Errorf("%s: %s", i18n.T("settings.lang_invalid"), strings.Join(supported, ", "))
	case "menu_layout":
		v := strings.ToLower(strings.TrimSpace(value))
		if v != "grouped" && v != "nested" {
			return errors.New(i18n.T("settings.menu_invalid"))
		}
		s.MenuLayout = v
		return nil
	case "gateway_region":
		v := strings.ToLower(strings.TrimSpace(value))
		switch v {
		case "", "global":
			s.GatewayRegion = "global"
			return nil
		case "cn", "china":
			s.GatewayRegion = "cn"
			return nil
		default:
			return errors.New("gateway_region must be global or cn")
		}
	}
	return fmt.Errorf(i18n.T("settings.unknown_key"), key)
}

// effectiveMenuLayout maps the stored value (possibly empty) to the
// concrete layout the launcher uses, so `settings get` / `list` show
// what's actually in effect rather than a blank.
func effectiveMenuLayout(v string) string {
	if v == "nested" {
		return "nested"
	}
	return "grouped"
}

// labelMenuLayout renders the layout for the settings list — the
// localized human name, not the raw enum value.
func labelMenuLayout(v string) string {
	if effectiveMenuLayout(v) == "nested" {
		return i18n.T("settings.menu_nested")
	}
	return i18n.T("settings.menu_grouped")
}

func labelGatewayRegion(v string) string {
	region := config.EffectiveGatewayRegion(v)
	if strings.TrimSpace(v) == "" {
		return fmt.Sprintf(i18n.T("settings.default_label"), region)
	}
	return region
}

// labelLanguage renders an unset Language as "(default)" + the
// detected fallback, so `settings list` is informative even when
// no preference is on disk.
func labelLanguage(v string) string {
	if v == "" {
		// Surface the active runtime language so the user sees
		// what we're actually using right now.
		live := i18n.Language()
		return fmt.Sprintf(i18n.T("settings.default_label"), live)
	}
	return v
}
