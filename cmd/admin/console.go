package admin

// console.go is the interactive `everyapi admin` operator console: a
// two-level picker (area → action) that prompts inline for any argument
// an action needs, then dispatches through Run exactly as if the operator
// had typed `everyapi admin <area> <action> <args…>`. It's the TTY
// counterpart to the typed subcommands — every admin verb is reachable,
// including the keyed ones (show <id>, manage <id> --action …) that can't
// be plain picker rows because they need a value.
//
// Navigation: Esc (or the trailing back row) climbs one level — action →
// area → exit. Running an action returns to its area menu so an operator
// can fire several in a row. Cancelling a prompt aborts just that action.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
)

// consoleAction is one row in an area's action picker. collect runs the
// inline prompts and returns the full argv to hand to Run (e.g.
// {"user","show","42"}); it returns cliprompt.ErrPickCancelled when the
// operator backs out of a prompt, which the caller treats as "no-op,
// re-render the menu".
type consoleAction struct {
	verb    string // English token shown in the picker — matches the typed arg
	descKey string // i18n key for the description column
	collect func(in *bufio.Reader) ([]string, error)
}

// consoleArea is a top-level entry. When actions is non-nil the operator
// drills into an action picker; when it's nil the area is a leaf that
// runs leafArgs immediately (audit, log — they have a single no-arg
// action, so a sub-menu would just be one row).
type consoleArea struct {
	name     string
	descKey  string
	actions  []consoleAction
	leafArgs []string
}

// consoleAreas is the operator console's full map, in display order.
func consoleAreas() []consoleArea {
	return []consoleArea{
		{name: "marketplace", descKey: "admin.area.marketplace", actions: []consoleAction{
			{verb: "status", descKey: "admin.act.marketplace_status", collect: noArgs("marketplace", "status")},
			{verb: "on", descKey: "admin.act.on", collect: noArgs("marketplace", "on")},
			{verb: "off", descKey: "admin.act.off", collect: noArgs("marketplace", "off")},
		}},
		{name: "user", descKey: "admin.area.user", actions: []consoleAction{
			{verb: "list", descKey: "admin.act.list", collect: noArgs("user", "list")},
			{verb: "search", descKey: "admin.act.search", collect: collectSearch("user")},
			{verb: "show", descKey: "admin.act.show", collect: collectID("user", "show")},
			{verb: "manage", descKey: "admin.act.manage", collect: collectUserManage},
			{verb: "delete", descKey: "admin.act.delete", collect: collectID("user", "delete")},
		}},
		{name: "channel", descKey: "admin.area.channel", actions: []consoleAction{
			{verb: "test", descKey: "admin.act.test", collect: collectID("channel", "test")},
			{verb: "tag", descKey: "admin.act.tag", collect: collectChannelTag},
		}},
		{name: "log", descKey: "admin.area.log", leafArgs: []string{"log", "tail"}},
		{name: "abuse", descKey: "admin.area.abuse", actions: []consoleAction{
			{verb: "list", descKey: "admin.act.list", collect: noArgs("abuse", "list")},
			{verb: "show", descKey: "admin.act.show", collect: collectID("abuse", "show")},
			{verb: "update", descKey: "admin.act.update", collect: collectAbuseUpdate},
		}},
		{name: "audit", descKey: "admin.area.audit", leafArgs: []string{"audit"}},
		{name: "redemption", descKey: "admin.area.redemption", actions: []consoleAction{
			{verb: "list", descKey: "admin.act.list", collect: noArgs("redemption", "list")},
			{verb: "search", descKey: "admin.act.search", collect: collectSearch("redemption")},
			{verb: "show", descKey: "admin.act.show", collect: collectID("redemption", "show")},
			{verb: "create", descKey: "admin.act.create", collect: collectRedemptionCreate},
			{verb: "update", descKey: "admin.act.update", collect: collectRedemptionUpdate},
			{verb: "status", descKey: "admin.act.status", collect: collectRedemptionStatus},
			{verb: "delete", descKey: "admin.act.delete", collect: collectID("redemption", "delete")},
			{verb: "clear-invalid", descKey: "admin.act.clear_invalid", collect: noArgs("redemption", "clear-invalid")},
		}},
	}
}

// runConsole drives the area → action loop. Returns nil on a clean exit
// (Esc at the area level); action errors are printed but never eject the
// operator, mirroring the launcher's "stay in the menu" rule.
func runConsole() error {
	in := bufio.NewReader(os.Stdin)
	areas := consoleAreas()
	w := 0
	for _, a := range areas {
		if len(a.name) > w {
			w = len(a.name)
		}
	}
	areaRows := make([]string, len(areas))
	for i, a := range areas {
		areaRows[i] = pad(a.name, w) + "  " + i18n.T(a.descKey)
	}
	areaRows = append(areaRows, backRow(w))
	for {
		idx, err := cliprompt.Pick(i18n.T("admin.console.title_area"), areaRows)
		if err != nil || idx == len(areas) { // Esc or back row
			return nil
		}
		a := areas[idx]
		if a.actions == nil { // leaf — run immediately
			reportErr(Run(a.leafArgs))
			continue
		}
		runArea(in, a)
	}
}

// runArea loops one area's action picker until the operator backs out.
func runArea(in *bufio.Reader, a consoleArea) {
	w := 0
	for _, act := range a.actions {
		if len(act.verb) > w {
			w = len(act.verb)
		}
	}
	rows := make([]string, len(a.actions))
	for i, act := range a.actions {
		rows[i] = pad(act.verb, w) + "  " + i18n.T(act.descKey)
	}
	rows = append(rows, backRow(w))
	title := fmt.Sprintf(i18n.T("admin.console.title_action"), a.name)
	for {
		idx, err := cliprompt.Pick(title, rows)
		if err != nil || idx == len(a.actions) { // Esc or back row
			return
		}
		args, cerr := a.actions[idx].collect(in)
		if errors.Is(cerr, cliprompt.ErrPickCancelled) {
			continue // operator backed out of a prompt
		}
		if cerr != nil {
			reportErr(cerr)
			continue
		}
		reportErr(Run(args))
	}
}

// --- prompt helpers ----------------------------------------------------

// noArgs is the collect func for actions that take no input.
func noArgs(args ...string) func(*bufio.Reader) ([]string, error) {
	return func(*bufio.Reader) ([]string, error) { return args, nil }
}

// collectID prompts for a positive integer id and assembles base + id.
func collectID(base ...string) func(*bufio.Reader) ([]string, error) {
	return func(in *bufio.Reader) ([]string, error) {
		id, err := promptID(in, "admin.prompt.id")
		if err != nil {
			return nil, err
		}
		return append(append([]string{}, base...), id), nil
	}
}

// collectSearch prompts for a keyword and assembles {area, "search", kw}.
func collectSearch(area string) func(*bufio.Reader) ([]string, error) {
	return func(in *bufio.Reader) ([]string, error) {
		kw, err := promptRequired(in, "admin.prompt.keyword")
		if err != nil {
			return nil, err
		}
		return []string{area, "search", kw}, nil
	}
}

func collectUserManage(in *bufio.Reader) ([]string, error) {
	id, err := promptID(in, "admin.prompt.id")
	if err != nil {
		return nil, err
	}
	action, err := promptChoice("admin.prompt.user_action",
		[]string{"enable", "disable", "delete", "promote_admin", "demote_admin"})
	if err != nil {
		return nil, err
	}
	return []string{"user", "manage", id, "--action", action}, nil
}

func collectChannelTag(in *bufio.Reader) ([]string, error) {
	name, err := promptRequired(in, "admin.prompt.name")
	if err != nil {
		return nil, err
	}
	state, err := promptChoice("admin.prompt.state", []string{"enable", "disable"})
	if err != nil {
		return nil, err
	}
	return []string{"channel", "tag", name, "--" + state}, nil
}

func collectAbuseUpdate(in *bufio.Reader) ([]string, error) {
	id, err := promptID(in, "admin.prompt.id")
	if err != nil {
		return nil, err
	}
	status, err := promptRequired(in, "admin.prompt.status")
	if err != nil {
		return nil, err
	}
	args := []string{"abuse", "update", id, "--status", status}
	note, err := promptOptional(in, "admin.prompt.note")
	if err != nil {
		return nil, err
	}
	if note != "" {
		args = append(args, "--note", note)
	}
	return args, nil
}

func collectRedemptionCreate(in *bufio.Reader) ([]string, error) {
	name, err := promptRequired(in, "admin.prompt.name")
	if err != nil {
		return nil, err
	}
	quota, err := promptRequired(in, "admin.prompt.quota")
	if err != nil {
		return nil, err
	}
	args := []string{"redemption", "create", "--name", name, "--quota", quota}
	count, err := promptOptional(in, "admin.prompt.count")
	if err != nil {
		return nil, err
	}
	if count != "" {
		args = append(args, "--count", count)
	}
	expires, err := promptOptional(in, "admin.prompt.expires")
	if err != nil {
		return nil, err
	}
	if expires != "" {
		args = append(args, "--expires", expires)
	}
	return args, nil
}

func collectRedemptionUpdate(in *bufio.Reader) ([]string, error) {
	id, err := promptID(in, "admin.prompt.id")
	if err != nil {
		return nil, err
	}
	args := []string{"redemption", "update", id}
	name, err := promptOptional(in, "admin.prompt.name")
	if err != nil {
		return nil, err
	}
	if name != "" {
		args = append(args, "--name", name)
	}
	quota, err := promptOptional(in, "admin.prompt.quota")
	if err != nil {
		return nil, err
	}
	if quota != "" {
		args = append(args, "--quota", quota)
	}
	expires, err := promptOptional(in, "admin.prompt.expires")
	if err != nil {
		return nil, err
	}
	if expires != "" {
		args = append(args, "--expires", expires)
	}
	return args, nil
}

func collectRedemptionStatus(in *bufio.Reader) ([]string, error) {
	id, err := promptID(in, "admin.prompt.id")
	if err != nil {
		return nil, err
	}
	state, err := promptChoice("admin.prompt.state", []string{"enable", "disable"})
	if err != nil {
		return nil, err
	}
	return []string{"redemption", "status", id, state}, nil
}

// promptRequired loops cliprompt.Line until a non-blank value is entered
// (or the operator cancels with Esc → ErrPickCancelled).
func promptRequired(in *bufio.Reader, labelKey string) (string, error) {
	for {
		v, err := cliprompt.Line(in, i18n.T(labelKey), "")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
	}
}

// promptOptional returns "" (skip the flag) when the operator enters a
// blank line, so optional flags stay omitted unless explicitly set.
func promptOptional(in *bufio.Reader, labelKey string) (string, error) {
	return cliprompt.LineOptional(in, i18n.T(labelKey))
}

// promptID is promptRequired plus a positive-integer check, re-prompting
// on a bad value so a typo doesn't abort the action.
func promptID(in *bufio.Reader, labelKey string) (string, error) {
	for {
		v, err := promptRequired(in, labelKey)
		if err != nil {
			return "", err
		}
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			return v, nil
		}
		cliout.Println(i18n.T("admin.prompt.bad_id"))
	}
}

// promptChoice renders a single-select picker over the fixed enum values.
func promptChoice(labelKey string, choices []string) (string, error) {
	idx, err := cliprompt.Pick(i18n.T(labelKey), choices)
	if err != nil {
		return "", err
	}
	return choices[idx], nil
}

// reportErr prints a non-cancel error to stderr without ejecting the
// operator from the console — same rule the launcher uses.
func reportErr(err error) {
	if err == nil || errors.Is(err, cliprompt.ErrPickCancelled) {
		return
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), err)
}

// backRow renders the trailing "go up a level" row in the same two-column
// shape as the entries above it (name column + hint), matching the
// launcher's sub-picker back row so the affordance reads identically.
func backRow(w int) string {
	return pad(i18n.T("common.back"), w) + "  " + i18n.T("common.back_hint")
}

// pad right-pads s to w display columns. Uses display width (not byte
// length) so the CJK localized back word ("返回") aligns its hint with
// the ASCII verb/area rows above it.
func pad(s string, w int) string {
	if d := w - style.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
