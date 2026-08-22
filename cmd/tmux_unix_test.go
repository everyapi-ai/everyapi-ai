//go:build !windows

package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
)

// inventoryLine builds one tmux -F line in the order tmuxInventoryFormat asks
// for, so a change to that format breaks these tests instead of silently
// reshuffling the fields the parser trusts.
func inventoryLine(name, created, activity, attached, dead, managed, path string) string {
	return strings.Join([]string{name, created, activity, attached, dead, managed, path}, "\t")
}

func TestParseTmuxInventoryKeepsOnlyManagedSessions(t *testing.T) {
	const v3 = "everyapi-v3-codex-3256d52ef013-8704e3c7ee40fd964014c218388c7be6"
	const legacy = "everyapi-23545-1787102440124"
	output := strings.Join([]string{
		inventoryLine(v3, "1700000000", "1700003600", "0", "0", "1", "/Users/e/work/api"),
		inventoryLine(legacy, "1699000000", "1699000060", "1", "0", "1", "/Users/e/work/old"),
		// A session whose name matches the glob but which EveryAPI never started.
		inventoryLine("everyapi-notes", "1700000000", "1700000000", "0", "0", "0", "/tmp"),
		// A pane the user split off inside a managed session: same session, but
		// not the pane that describes it.
		inventoryLine(v3, "1700000000", "1700003600", "0", "0", "0", "/elsewhere"),
	}, "\n") + "\n"

	sessions, err := parseTmuxInventory(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(sessions), sessions)
	}
	// Most recently active first.
	if sessions[0].name != v3 || sessions[1].name != legacy {
		t.Fatalf("order = %q, %q; want the most recently active first", sessions[0].name, sessions[1].name)
	}
	if sessions[0].tool != "codex" {
		t.Fatalf("tool = %q, want codex", sessions[0].tool)
	}
	if sessions[0].directory != "/Users/e/work/api" {
		t.Fatalf("directory = %q, want the pane's path", sessions[0].directory)
	}
	if sessions[0].attached || sessions[0].dead || sessions[0].legacy {
		t.Fatalf("v3 session flags = attached:%v dead:%v legacy:%v, want all false", sessions[0].attached, sessions[0].dead, sessions[0].legacy)
	}
	if !sessions[1].attached || !sessions[1].legacy {
		t.Fatalf("legacy session flags = attached:%v legacy:%v, want both true", sessions[1].attached, sessions[1].legacy)
	}
}

// A path may contain spaces and even the separator-adjacent characters, so it
// has to absorb the rest of the line rather than be split out of the middle.
func TestParseTmuxInventoryKeepsWholePathWithSpaces(t *testing.T) {
	const name = "everyapi-v3-claude-3256d52ef013-8704e3c7ee40fd964014c218388c7be6"
	const path = "/Users/e/My Projects/every api"
	sessions, err := parseTmuxInventory(inventoryLine(name, "1700000000", "1700000000", "0", "0", "1", path) + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].directory != path {
		t.Fatalf("directory = %+v, want %q intact", sessions, path)
	}
}

// A malformed line means the format contract broke. Dropping it would make the
// listing quietly incomplete, and an incomplete listing is worse than an error
// here: the user would conclude a session is gone and start a duplicate.
func TestParseTmuxInventoryRejectsMalformedLines(t *testing.T) {
	const name = "everyapi-v3-codex-3256d52ef013-8704e3c7ee40fd964014c218388c7be6"
	tests := map[string]string{
		"too few fields":   "everyapi-v3-codex-3256d52ef013-8704e3c7ee40fd964014c218388c7be6\t1700000000\t1\t0",
		"bad created":      inventoryLine(name, "yesterday", "1700000000", "0", "0", "1", "/tmp"),
		"bad activity":     inventoryLine(name, "1700000000", "soon", "0", "0", "1", "/tmp"),
		"bad client count": inventoryLine(name, "1700000000", "1700000000", "several", "0", "1", "/tmp"),
		"bad dead flag":    inventoryLine(name, "1700000000", "1700000000", "0", "yes", "1", "/tmp"),
	}
	for label, line := range tests {
		t.Run(label, func(t *testing.T) {
			if _, err := parseTmuxInventory(line + "\n"); err == nil {
				t.Fatal("malformed inventory line accepted")
			}
		})
	}
}

// session_attached is the count of attached clients, not a flag. A session open
// in two terminals reports "2"; treating that as malformed would fail the whole
// listing — every session, not just the doubly-attached one.
func TestParseTmuxInventoryAcceptsSeveralAttachedClients(t *testing.T) {
	const name = "everyapi-v3-codex-3256d52ef013-8704e3c7ee40fd964014c218388c7be6"
	sessions, err := parseTmuxInventory(inventoryLine(name, "1700000000", "1700000000", "2", "0", "1", "/tmp") + "\n")
	if err != nil {
		t.Fatalf("two attached clients rejected: %v", err)
	}
	if len(sessions) != 1 || !sessions[0].attached {
		t.Fatalf("sessions = %+v, want one session marked attached", sessions)
	}
}

// The pre-v3 name shapes — both everyapi-v2- and the oldest everyapi-<pid>-<ts>
// — are ours but unreachable from any launch, which is what `legacy` reports.
func TestParseTmuxInventoryMarksEveryPreV3ShapeLegacy(t *testing.T) {
	lines := map[string]bool{
		"everyapi-v3-codex-3256d52ef013-8704e3c7ee40fd964014c218388c7be6": false,
		"everyapi-v2-codex-3256d52ef013-4242-1787102440124":               true,
		"everyapi-23545-1787102440124":                                    true,
	}
	for name, wantLegacy := range lines {
		sessions, err := parseTmuxInventory(inventoryLine(name, "1700000000", "1700000000", "0", "0", "1", "/tmp") + "\n")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(sessions) != 1 {
			t.Fatalf("%s: got %d sessions, want 1", name, len(sessions))
		}
		if sessions[0].legacy != wantLegacy {
			t.Errorf("%s: legacy = %v, want %v", name, sessions[0].legacy, wantLegacy)
		}
	}
}

func TestParseTmuxInventoryHandlesNoSessions(t *testing.T) {
	sessions, err := parseTmuxInventory("")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("got %d sessions from empty output", len(sessions))
	}
}

func TestTmuxSessionToolAndWorkspaceSplitsBothGenerations(t *testing.T) {
	tests := []struct {
		name          string
		wantTool      string
		wantWorkspace string
		wantOK        bool
	}{
		{name: "everyapi-v3-codex-3256d52ef013-8704e3c7ee40fd964014c218388c7be6", wantTool: "codex", wantWorkspace: "3256d52ef013", wantOK: true},
		{name: "everyapi-v3-qwen-code-3256d52ef013-8704e3c7ee40fd964014c218388c7be6", wantTool: "qwen-code", wantWorkspace: "3256d52ef013", wantOK: true},
		{name: "everyapi-v2-codex-3256d52ef013-4242-1787102440124", wantTool: "codex", wantWorkspace: "3256d52ef013", wantOK: true},
		// The oldest shape carries neither tool nor workspace, so it is not a
		// generated name by this parser's contract even though it is ours.
		{name: "everyapi-23545-1787102440124", wantOK: false},
		{name: "my-own-session", wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool, workspace, ok := tmuxSessionToolAndWorkspace(test.name)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}
			if tool != test.wantTool || workspace != test.wantWorkspace {
				t.Fatalf("tool/workspace = %q/%q, want %q/%q", tool, workspace, test.wantTool, test.wantWorkspace)
			}
		})
	}
}

// A session detached seconds ago must not read as stale, and a multi-day one
// must not collapse into an hour count nobody can parse at a glance.
func TestTmuxIdleLabelScalesWithAge(t *testing.T) {
	now := time.Date(2026, 8, 22, 19, 35, 0, 0, time.UTC)
	tests := []struct {
		idle time.Duration
		want string
	}{
		{idle: 0, want: "now"},
		{idle: 30 * time.Second, want: "now"},
		{idle: 90 * time.Second, want: "1m"},
		{idle: 59 * time.Minute, want: "59m"},
		{idle: 90 * time.Minute, want: "1h30m"},
		{idle: 25 * time.Hour, want: "1d1h"},
		{idle: 67*time.Hour + 11*time.Minute, want: "2d19h"},
	}
	for _, test := range tests {
		if got := tmuxIdleLabel(now.Add(-test.idle), now); got != test.want {
			t.Errorf("idle %s = %q, want %q", test.idle, got, test.want)
		}
	}
}

// The rows the picker offers are the rows the listing prints, so they have to
// line up: one shared layout, columns padded to the widest cell.
func TestTmuxSessionRowsAlignColumnsAndNameTheWorkspace(t *testing.T) {
	now := time.Date(2026, 8, 22, 19, 35, 0, 0, time.UTC)
	sessions := []managedTmuxSession{
		{name: "a", tool: "codex", directory: "/w/api", activity: now.Add(-2 * time.Hour)},
		{name: "b", tool: "claude", directory: "/w/dashboard", activity: now.Add(-90 * time.Hour)},
	}
	rows := tmuxSessionRows(sessions, now)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !strings.HasPrefix(rows[0], "codex ") {
		t.Fatalf("row = %q, want the shorter tool padded to the wider one", rows[0])
	}
	// Every column before the workspace is padded, so the workspace starts at
	// the same offset on every row even when the state labels differ in length.
	mixed := tmuxSessionRows([]managedTmuxSession{
		{name: "a", tool: "codex", directory: "/w/api", activity: now, dead: true},
		{name: "b", tool: "codex", directory: "/w/dash", activity: now},
	}, now)
	if a, b := style.Width(mixed[0])-style.Width("/w/api"), style.Width(mixed[1])-style.Width("/w/dash"); a != b {
		t.Fatalf("workspace column starts at %d on the dead row and %d on the detached one: %q / %q", a, b, mixed[0], mixed[1])
	}
	for index, session := range sessions {
		if !strings.Contains(rows[index], session.directory) {
			t.Fatalf("row %q does not name workspace %q", rows[index], session.directory)
		}
	}
	// A dead session must be distinguishable from a merely detached one, or the
	// user attaches to a frozen pane and reads it as a hang.
	dead := tmuxSessionRows([]managedTmuxSession{{tool: "codex", dead: true, activity: now}}, now)[0]
	detached := tmuxSessionRows([]managedTmuxSession{{tool: "codex", activity: now}}, now)[0]
	if dead == detached {
		t.Fatalf("dead and detached rows render identically: %q", dead)
	}
}

// Connect renders the list in its own window, so the JSON shape is a contract.
func TestMachineTmuxSessionShapeIsStable(t *testing.T) {
	session := managedTmuxSession{
		name:      "everyapi-v3-codex-3256d52ef013-8704e3c7ee40fd964014c218388c7be6",
		tool:      "codex",
		directory: "/w/api",
		created:   time.Unix(1700000000, 0),
		activity:  time.Unix(1700003600, 0),
		attached:  true,
		dead:      false,
		legacy:    false,
	}
	data, err := json.Marshal(machineTmuxSession{
		Name:        session.name,
		Tool:        session.tool,
		Directory:   session.directory,
		Created:     session.created.Format(time.RFC3339),
		LastActive:  session.activity.Format(time.RFC3339),
		IdleSeconds: 42,
		Attached:    session.attached,
		Dead:        session.dead,
		Legacy:      session.legacy,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "tool", "directory", "created", "last_active", "idle_seconds", "attached", "dead", "legacy"} {
		if !strings.Contains(string(data), `"`+field+`"`) {
			t.Errorf("JSON is missing field %q: %s", field, data)
		}
	}
}

// The inventory query and the launch path's reuse query must agree on what
// counts as a pane EveryAPI started; two copies of that predicate would drift.
func TestTmuxInventoryFormatSharesTheManagedPanePredicate(t *testing.T) {
	format := tmuxInventoryFormat()
	if !strings.Contains(format, tmuxManagedPaneFormat()) {
		t.Fatalf("inventory format %q does not reuse the managed-pane predicate %q", format, tmuxManagedPaneFormat())
	}
	// pane_current_path must stay last: a path can contain anything but a tab,
	// so it has to absorb the remainder of the line.
	if !strings.HasSuffix(format, "#{pane_current_path}") {
		t.Fatalf("inventory format %q must end with pane_current_path", format)
	}
}

// Copying a raw `tmux attach` out of the launch banner while still inside tmux
// is a refusal ("sessions should be nested with care"); this command has to work
// from either side.
func TestTmuxAttachActionSwitchesClientInsideTmux(t *testing.T) {
	if got := tmuxAttachAction(""); got != "attach-session" {
		t.Fatalf("outside tmux = %q, want attach-session", got)
	}
	if got := tmuxAttachAction("/private/tmp/tmux-501/default,4242,0"); got != "switch-client" {
		t.Fatalf("inside tmux = %q, want switch-client", got)
	}
}

// Every other destructive command in this CLI routes its confirm flag through
// cliprompt.SplitConfirmFlag so the accepted spellings stay identical. A user
// whose habit is `token revoke 5 -y` must not hit "flag provided but not
// defined" here, and the flag has to work after the session name too.
func TestTmuxKillAcceptsEveryConfirmSpellingInAnyPosition(t *testing.T) {
	const name = "everyapi-v3-codex-3256d52ef013-8704e3c7ee40fd964014c218388c7be6"
	for _, spelling := range []string{"-y", "--y", "-yes", "--yes"} {
		for _, args := range [][]string{{name, spelling}, {spelling, name}} {
			skip, positional := cliprompt.SplitConfirmFlag(args)
			if !skip {
				t.Errorf("%v: confirm flag not recognized", args)
			}
			if len(positional) != 1 || positional[0] != name {
				t.Errorf("%v: positional = %v, want just the session name", args, positional)
			}
		}
	}
}

// --all with nobody to ask must refuse rather than proceed, or `kill --all |
// tee log` typed at a real prompt mass-kills every agent in silence.
//
// This exercises the decision only. Nothing here may call runTmuxKill: it acts
// on the real tmux server, and a test that reached it once destroyed 30 live
// sessions on a developer machine.
func TestTmuxKillAuthorizationRefusesUnconfirmedAll(t *testing.T) {
	tests := []struct {
		name        string
		yes, all    bool
		interactive bool
		wantPrompt  bool
		wantError   bool
	}{
		{name: "terminal asks", interactive: true, wantPrompt: true},
		{name: "terminal asks for all", all: true, interactive: true, wantPrompt: true},
		{name: "confirmed skips the prompt", yes: true, interactive: true},
		{name: "piped all is refused", all: true, wantError: true},
		{name: "piped all with --yes proceeds", all: true, yes: true},
		// A named session is already an explicit statement of intent, so a
		// script may close one without a terminal to confirm on.
		{name: "piped named session proceeds", interactive: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompt, err := tmuxKillAuthorization(test.yes, test.all, test.interactive)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError = %v", err, test.wantError)
			}
			if err != nil && !strings.Contains(err.Error(), i18n.T("tmux.kill_all_needs_confirmation")) {
				t.Fatalf("error = %v, want the --all confirmation refusal", err)
			}
			if prompt != test.wantPrompt {
				t.Fatalf("prompt = %v, want %v", prompt, test.wantPrompt)
			}
		})
	}
}

// `attach` takes a bare name, so there is no FlagSet to turn -h into usage.
func TestTmuxAttachTreatsHelpTokensAsHelp(t *testing.T) {
	for _, token := range []string{"help", "--help", "-h"} {
		if err := runTmuxAttach([]string{token}); err != nil {
			t.Errorf("attach %s = %v, want usage", token, err)
		}
	}
}

// "There is no server" is a legitimate answer to a listing. Anything else tmux
// says is a failure, and reporting it as an empty list is the "your sessions are
// gone" misreading the Windows stub exists to avoid.
func TestTmuxServerAbsentSeparatesNoServerFromRealFailures(t *testing.T) {
	absent := []string{
		"",
		"   \n",
		"no server running on /private/tmp/tmux-501/default",
		"error connecting to /private/tmp/tmux-501/default (No such file or directory)",
	}
	for _, stderr := range absent {
		if !tmuxServerAbsent([]byte(stderr)) {
			t.Errorf("stderr %q treated as a failure, want no-server", stderr)
		}
	}
	failures := []string{
		"command list-panes: invalid flag --",
		"unknown option: -f",
		"usage: list-panes [-as]",
	}
	for _, stderr := range failures {
		if tmuxServerAbsent([]byte(stderr)) {
			t.Errorf("stderr %q treated as no-server, want a reported failure", stderr)
		}
	}
}

// A dead pane reports no cwd, so that row would end after the state column and
// read as truncated rather than as "there is nothing here".
func TestTmuxSessionRowsLabelTheMissingDirectory(t *testing.T) {
	now := time.Date(2026, 8, 22, 19, 35, 0, 0, time.UTC)
	row := tmuxSessionRows([]managedTmuxSession{{tool: "codex", dead: true, activity: now}}, now)[0]
	if !strings.Contains(row, i18n.T("tmux.no_directory")) {
		t.Fatalf("row = %q, want the missing-directory label", row)
	}
	// A session that does have one still shows it verbatim.
	row = tmuxSessionRows([]managedTmuxSession{{tool: "codex", directory: "/w/api", activity: now}}, now)[0]
	if !strings.Contains(row, "/w/api") || strings.Contains(row, i18n.T("tmux.no_directory")) {
		t.Fatalf("row = %q, want the real directory and no placeholder", row)
	}
}
