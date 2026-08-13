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
	"strconv"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/cmd/token"
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
	if len(args) > 1 && (args[1] == "help" || args[1] == "--help" || args[1] == "-h") {
		cliout.Println(i18n.T("settings.usage"))
		return nil
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
	cliout.Printf("  %s: %s\n", i18n.T("settings.gateway_region_label"), labelGatewayRegion(s.GatewayRegion))
	cliout.Printf("  %s: %s\n", i18n.T("settings.codex_bypass_label"), displayOptionalBool(s.CodexHookTrustBypass))
	cliout.Printf("  %s: %s\n", i18n.T("settings.dangerous_mode_label"), displayOptionalBool(s.DangerousMode))
	path, _ := config.SettingsPath()
	if path != "" {
		cliout.Printf("\n%s %s\n", i18n.T("settings.file_at"), path)
	}
	return nil
}

func runGet(args []string) error {
	if len(args) != 1 {
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
	if len(args) != 2 {
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
	if fs.NArg() != 0 {
		return errors.New("usage: everyapi settings reset [-y]")
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

// settingRow is one line of the editor: its name, how to render the value in
// effect, and how to change it. Rendering and dispatch read the same list, so
// a preference cannot show up in one and not the other. The editor used to ask
// two hard-coded questions in a fixed order, which left gateway_region,
// codex_hook_trust_bypass and dangerous_mode reachable only through
// `settings set`, and gave no way at all to see or change the default relay
// key — the one setting that decides which models every launch can reach.
type settingRow struct {
	// key is the settings.json key this row edits, and is empty for a row
	// backed by something else (the relay key lives in credentials.json).
	// TestSettingRowsCoverEverySettingsKey reads it to prove a new key cannot
	// be added to the file without also appearing here.
	key   string
	label string
	value func(*config.Settings) string
	edit  func(*config.Settings) error
}

func settingRows() []settingRow {
	return []settingRow{
		{"language", i18n.T("settings.lang_label"), func(s *config.Settings) string { return labelLanguage(s.Language) }, editLanguage},
		{"menu_layout", i18n.T("settings.menu_label"), func(s *config.Settings) string { return labelMenuLayout(s.MenuLayout) }, editMenuLayout},
		{"gateway_region", i18n.T("settings.gateway_region_label"), func(s *config.Settings) string { return labelGatewayRegion(s.GatewayRegion) }, editGatewayRegion},
		{"codex_hook_trust_bypass", i18n.T("settings.codex_bypass_label"), func(s *config.Settings) string { return displayOptionalBool(s.CodexHookTrustBypass) }, editCodexHookTrustBypass},
		{"dangerous_mode", i18n.T("settings.dangerous_mode_label"), func(s *config.Settings) string { return displayOptionalBool(s.DangerousMode) }, editDangerousMode},
		{"", i18n.T("settings.default_key_label"), func(*config.Settings) string { return labelDefaultRelayKey() }, editDefaultRelayKey},
	}
}

func runInteractive() error {
	selected := 0
	for {
		// Reload every pass: an editor may have written the file (and the
		// key editor writes credentials.json out from under us), so the menu
		// must re-read rather than render a stale copy of what it just saved.
		s, err := config.LoadSettings()
		if err != nil {
			return err
		}
		rows := settingRows()
		labels := make([]string, 0, len(rows)+1)
		for _, row := range rows {
			labels = append(labels, fmt.Sprintf("%s: %s", row.label, row.value(s)))
		}
		labels = append(labels, i18n.T("settings.done"))

		idx, err := cliprompt.PickWithSelected(i18n.T("settings.menu_pick"), labels, selected)
		if err != nil {
			// Esc / Ctrl-C at the menu leaves everything as it stands; each
			// editor persists its own change, so there is nothing pending.
			if errors.Is(err, cliprompt.ErrPickCancelled) {
				return nil
			}
			return err
		}
		if idx == len(rows) {
			return nil
		}
		selected = idx
		if err := rows[idx].edit(s); err != nil {
			// Esc inside an editor means "changed my mind about this one",
			// not "quit" — go back to the menu with nothing written.
			if errors.Is(err, cliprompt.ErrPickCancelled) {
				continue
			}
			return err
		}
	}
}

// Build options from the live SupportedLanguages list (sorted en first by the
// loader) so a dropped-in {lang}.toml shows up automatically. Each row is
// "<code> — <native name>" — the native name comes from a small lookup table
// because we'd otherwise need a "language.native_name" key in every locale
// just for self-labelling.
func editLanguage(s *config.Settings) error {
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
		return err
	}
	s.Language = langs[idx]
	// Apply immediately so the menu redraws in the just-chosen language.
	i18n.SetLanguage(s.Language)
	// Export the resolved canonical tag (normalized), not the raw pick.
	_ = os.Setenv("EVERYAPI_LANG", i18n.Language())
	return saveAndReport(s)
}

func editMenuLayout(s *config.Settings) error {
	layouts := []string{"grouped", "nested"}
	opts := []string{i18n.T("settings.menu_grouped"), i18n.T("settings.menu_nested")}
	cur := 0
	if effectiveMenuLayout(s.MenuLayout) == "nested" {
		cur = 1
	}
	idx, err := cliprompt.PickWithSelected(i18n.T("settings.menu_label"), opts, cur)
	if err != nil {
		return err
	}
	s.MenuLayout = layouts[idx]
	return saveAndReport(s)
}

func editGatewayRegion(s *config.Settings) error {
	regions := []string{"global", "cn"}
	cur := 0
	if config.EffectiveGatewayRegion(s.GatewayRegion) == "cn" {
		cur = 1
	}
	idx, err := cliprompt.PickWithSelected(i18n.T("settings.gateway_region_label"), regions, cur)
	if err != nil {
		return err
	}
	s.GatewayRegion = regions[idx]
	return saveAndReport(s)
}

func editCodexHookTrustBypass(s *config.Settings) error {
	return editOptionalBool(s, i18n.T("settings.codex_bypass_label"), s.CodexHookTrustBypass, func(v *bool) { s.CodexHookTrustBypass = v })
}

func editDangerousMode(s *config.Settings) error {
	return editOptionalBool(s, i18n.T("settings.dangerous_mode_label"), s.DangerousMode, func(v *bool) { s.DangerousMode = v })
}

// editOptionalBool keeps the third state. These preferences distinguish "not
// set" (ask on first interactive use) from an explicit false, so the editor
// has to offer unset as a choice rather than collapsing it into off.
func editOptionalBool(s *config.Settings, label string, current *bool, apply func(*bool)) error {
	opts := []string{i18n.T("settings.bool_on"), i18n.T("settings.bool_off"), i18n.T("settings.unset")}
	cur := 2
	if current != nil {
		cur = 1
		if *current {
			cur = 0
		}
	}
	idx, err := cliprompt.PickWithSelected(label, opts, cur)
	if err != nil {
		return err
	}
	switch idx {
	case 0:
		apply(boolPtr(true))
	case 1:
		apply(boolPtr(false))
	default:
		apply(nil)
	}
	return saveAndReport(s)
}

func boolPtr(v bool) *bool { return &v }

// The default relay key lives in credentials.json, not settings.json — it is a
// credential, and this file is world-readable preference state. The editor
// shows which key is in effect and hands the change to the token picker, which
// owns fetching the key material and persisting it.
func labelDefaultRelayKey() string {
	creds, err := config.Load()
	if err != nil || creds == nil || creds.RelayKeyTokenID == 0 {
		return i18n.T("settings.default_key_none")
	}
	return fmt.Sprintf("#%d", creds.RelayKeyTokenID)
}

func editDefaultRelayKey(*config.Settings) error {
	return token.SwitchDefaultKey()
}

func saveAndReport(s *config.Settings) error {
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
	case "codex_hook_trust_bypass":
		return labelOptionalBool(s.CodexHookTrustBypass), true
	case "dangerous_mode":
		return labelOptionalBool(s.DangerousMode), true
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
	case "codex_hook_trust_bypass", "dangerous_mode":
		v, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("%s must be true or false", key)
		}
		if key == "codex_hook_trust_bypass" {
			s.CodexHookTrustBypass = &v
		} else {
			s.DangerousMode = &v
		}
		return nil
	}
	return fmt.Errorf(i18n.T("settings.unknown_key"), key)
}

// labelOptionalBool is the MACHINE form, and stays in English on purpose:
// `settings get` is a scripting interface, and `settings set` accepts exactly
// these words back. Display surfaces use displayOptionalBool instead.
func labelOptionalBool(value *bool) string {
	if value == nil {
		return "unset"
	}
	return strconv.FormatBool(*value)
}

// displayOptionalBool is the HUMAN form for the list and the editor, where
// "true" next to a translated label was the only English left on the screen.
func displayOptionalBool(value *bool) string {
	switch {
	case value == nil:
		return i18n.T("settings.unset")
	case *value:
		return i18n.T("settings.bool_on")
	default:
		return i18n.T("settings.bool_off")
	}
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
