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
	"slices"
	"strconv"
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

type settingRow struct {
	key   string
	value string
}

// settingsKeyWidth is the width of the key column in `settings list`:
// the longest key plus one space, derived from the rows themselves
// rather than hardcoded, since a key longer than today's longest would
// otherwise push its value out of the column with nothing to catch it.
// Keys are ASCII, so len is the display width.
func settingsKeyWidth(rows []settingRow) int {
	width := 0
	for _, row := range rows {
		if n := len(row.key); n > width {
			width = n
		}
	}
	return width + 1
}

// settingsRows is the single source of the listing order, shared with
// the interactive editor below so both surfaces cover the same keys in
// the same sequence.
func settingsRows(s *config.Settings) []settingRow {
	return []settingRow{
		{"language", labelLanguage(s.Language)},
		{"menu_layout", labelMenuLayout(s.MenuLayout)},
		{"gateway_region", labelGatewayRegion(s.GatewayRegion)},
		{"dangerous_mode", labelTriState(s.DangerousMode)},
		{"codex_hook_trust_bypass", labelTriState(s.CodexHookTrustBypass)},
	}
}

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
	// Rows are labelled with the literal key `settings get`/`set` takes,
	// not a translated name: the listing is where users discover the key
	// names, and a localized label can't be pasted back into `set`. The
	// values stay localized — those are read, not retyped.
	rows := settingsRows(s)
	width := settingsKeyWidth(rows)
	for _, row := range rows {
		cliout.Printf("  %-*s %s\n", width, row.key, row.value)
	}
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

func runInteractive() error {
	s, err := config.LoadSettings()
	if err != nil {
		return err
	}
	langs, langOptions, langCurrent := languageChoices(s.Language)
	idx, err := cliprompt.PickWithSelected(i18n.T("settings.lang_label"), langOptions, langCurrent)
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
	// Apply the language immediately so every picker below renders its
	// labels in the just-chosen language.
	i18n.SetLanguage(picked)
	// Export the resolved canonical tag (normalized), not the raw pick.
	_ = os.Setenv("EVERYAPI_LANG", i18n.Language())

	// Cancelling any question below leaves without writing the file, same
	// as cancelling the language picker above. cliprompt binds Esc and
	// Ctrl-C to one signal (ErrPickCancelled), so treating a cancel as
	// "skip this question" would leave the user with no way out of a
	// five-question walk — Ctrl-C would advance it instead of ending it,
	// and the file would still be written at the end. Skipping needs no
	// key of its own: every picker opens on the stored value, so Enter
	// already means "leave this one alone".
	for _, step := range editorSteps(s) {
		pick, perr := cliprompt.PickWithSelected(step.label, step.options, step.current)
		if perr != nil {
			if errors.Is(perr, cliprompt.ErrPickCancelled) {
				cliout.Println(i18n.T("common.canceled"))
				return nil
			}
			return perr
		}
		step.apply(pick)
	}

	if err := config.SaveSettings(s); err != nil {
		return err
	}
	cliout.Println(i18n.T("settings.saved"))
	return nil
}

// editorStep is one question in the interactive editor: a localized
// prompt, its rows, the row matching what is stored today, and the
// write-back for whichever row the user picks.
type editorStep struct {
	label   string
	options []string
	current int
	apply   func(int)
}

// editorSteps are the questions after the language picker, in the order
// `settings list` prints them. Built (not declared) so every label is
// rendered after the language pick has been applied.
//
// The two tri-state keys matter most here: `everyapi use` asks for them
// once at launch and then never mentions them again, so without a row in
// this editor a yes answered months ago is unreachable except by
// guessing the key name for `settings set`.
func editorSteps(s *config.Settings) []editorStep {
	layouts := []string{"grouped", "nested"}
	regions, regionOptions := gatewayRegionRows()
	return []editorStep{
		{
			label:   i18n.T("settings.menu_label"),
			options: []string{i18n.T("settings.menu_grouped"), i18n.T("settings.menu_nested")},
			current: slices.Index(layouts, effectiveMenuLayout(s.MenuLayout)),
			apply:   func(i int) { s.MenuLayout = layouts[i] },
		},
		{
			label:   i18n.T("settings.region_label"),
			options: regionOptions,
			current: gatewayRegionIndex(s.GatewayRegion),
			apply:   func(i int) { s.GatewayRegion = regions[i] },
		},
		{
			label:   i18n.T("settings.dangerous_label"),
			options: triStateOptions(),
			current: triStateIndex(s.DangerousMode),
			apply:   func(i int) { s.DangerousMode = triStateValue(i) },
		},
		{
			label:   i18n.T("settings.hook_trust_label"),
			options: triStateOptions(),
			current: triStateIndex(s.CodexHookTrustBypass),
			apply:   func(i int) { s.CodexHookTrustBypass = triStateValue(i) },
		},
	}
}

// gatewayRegionRows is the gateway_region table, shared by the picker
// and the list label so the two surfaces cannot word the same value
// differently. Row 0 is the stored-empty state and has to stay
// reachable: `everyapi login` asks for the region exactly once and only
// while the preference is empty (ensureGatewayRegionPreference), so
// collapsing "not chosen yet" into an explicit "global" would retire
// that prompt for anyone who ever walked this editor — including the
// mainland-China user who opened it only to change the language.
// The endpoints are formatted in from the SDK constants rather than
// spelled out in each locale: the same two hostnames would otherwise sit
// as literal text in sixteen translated strings, and a gateway move
// would leave every one of them advertising the old host.
func gatewayRegionRows() (values, options []string) {
	return []string{"", "global", "cn"}, []string{
		fmt.Sprintf(i18n.T("settings.default_label"), config.EffectiveGatewayRegion("")),
		fmt.Sprintf(i18n.T("settings.region_global"), config.DefaultAPIBase),
		fmt.Sprintf(i18n.T("settings.region_cn"), config.ChinaAPIBase),
	}
}

func gatewayRegionIndex(stored string) int {
	if strings.TrimSpace(stored) == "" {
		return 0
	}
	if config.EffectiveGatewayRegion(stored) == "cn" {
		return 2
	}
	return 1
}

// languageChoices builds the language picker rows from the live
// SupportedLanguages list (sorted en first by the loader) so a
// dropped-in {lang}.toml shows up automatically. Each row is
// "<code> — <native name>" — the native name comes from a small lookup
// table because we'd otherwise need a "language.native_name" key in
// every locale just for self-labelling. selected is the row holding
// `current`, or 0 when no preference is stored.
func languageChoices(current string) (langs, options []string, selected int) {
	langs = i18n.SupportedLanguages()
	nativeName := map[string]string{
		"en":    "English",
		"zh":    "中文",
		"zh-TW": "繁體中文",
		"ja":    "日本語",
		"ko":    "한국어",
		"es":    "Español",
		"de":    "Deutsch",
		"fr":    "Français",
	}
	options = make([]string, len(langs))
	for i, l := range langs {
		name := nativeName[l]
		if name == "" {
			name = l
		}
		options[i] = fmt.Sprintf("%s — %s", l, name)
		if l == current {
			selected = i
		}
	}
	return langs, options, selected
}

// --- key plumbing ---------------------------------------------------

// readKey + writeKey centralise the "key string ↔ struct field"
// mapping for `settings get`/`set`. Keep every key here reachable from
// settingsRows too, or it goes back to being settable but invisible.
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
		// "unset" is the third state `get` and `list` already print, and
		// the one the editor can pick. Accept it here too, or `get` emits
		// a value `set` refuses and the only way back to "not chosen yet"
		// is the TTY editor — unreachable over a pipe, where bare
		// `everyapi settings` lists instead of editing.
		raw := strings.TrimSpace(value)
		var parsed *bool
		if !strings.EqualFold(raw, "unset") {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("%s must be true, false or unset", key)
			}
			parsed = &v
		}
		if key == "codex_hook_trust_bypass" {
			s.CodexHookTrustBypass = parsed
		} else {
			s.DangerousMode = parsed
		}
		return nil
	}
	return fmt.Errorf(i18n.T("settings.unknown_key"), key)
}

// labelOptionalBool keeps `settings get` machine-readable: the literal
// a script can compare against, never a translated phrase.
func labelOptionalBool(value *bool) string {
	if value == nil {
		return "unset"
	}
	return strconv.FormatBool(*value)
}

// Tri-state ordering, shared by the picker rows, the index lookup and
// the list labels so those three can never drift: 0 = unset (asked once
// at launch), 1 = true, 2 = false.
func triStateOptions() []string {
	return []string{
		i18n.T("settings.tristate_unset"),
		i18n.T("settings.tristate_true"),
		i18n.T("settings.tristate_false"),
	}
}

func triStateIndex(value *bool) int {
	switch {
	case value == nil:
		return 0
	case *value:
		return 1
	default:
		return 2
	}
}

func triStateValue(idx int) *bool {
	switch idx {
	case 1:
		v := true
		return &v
	case 2:
		v := false
		return &v
	default:
		// Back to "not chosen yet", which is not the same as false:
		// `everyapi use` asks again on the next launch.
		return nil
	}
}

// labelTriState is the human half of labelOptionalBool — the literal
// `settings set` takes plus what it means — for `settings list`.
func labelTriState(value *bool) string {
	return triStateOptions()[triStateIndex(value)]
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

// labelGatewayRegion renders the stored region through the same table
// the picker shows, so `settings list` and the editor never disagree.
func labelGatewayRegion(v string) string {
	_, options := gatewayRegionRows()
	return options[gatewayRegionIndex(v)]
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
