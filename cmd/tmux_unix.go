//go:build !windows

package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
)

// managedTmuxSession is one EveryAPI-launched tmux session as the `tmux` command
// sees it. relaunchUseInTmux hands the user a reattach hint exactly once, at
// launch; after that scrollback is gone the session's only remaining handle is a
// 32-hex-character name in `tmux ls` output that says nothing about which
// project it belongs to. These fields are what makes a session recognizable
// again: the tool that is running, the directory it is running in, and how long
// it has been since anyone touched it.
type managedTmuxSession struct {
	name string
	// tool is parsed back out of the session name rather than read from the
	// running process: the pane's foreground command is EveryAPI's own wrapper
	// (tools.Exec deliberately leaves the tool in the wrapper's process group,
	// so tmux reports the group leader), which would name every session
	// "everyapi".
	tool string
	// directory is the pane's live working directory. The session name carries
	// only a hash of the launch directory's dev:ino, which is unreadable and
	// cannot be reversed, so this is the only human-facing workspace identity
	// available.
	directory string
	created   time.Time
	activity  time.Time
	attached  bool
	// dead marks a session whose managed pane has exited but which tmux kept
	// because the user enabled remain-on-exit. Launches prune these on sight;
	// listing them explains a session that is present but unusable.
	dead bool
	// legacy marks the pre-v3 name shapes. They are recognized as EveryAPI's
	// own, but no launch will ever reuse them, so they can only ever be
	// reattached or killed from here.
	legacy bool
}

// tmuxSessionToolAndWorkspace splits a managed session name back into its tool
// and workspace-hash components. Reported separately from the validity check so
// callers can group by workspace without re-deriving the split.
func tmuxSessionToolAndWorkspace(name string) (tool, workspace string, ok bool) {
	if !generatedEveryAPITmuxSession(name) {
		return "", "", false
	}
	prefix := managedTmuxPrefix
	suffixParts := 2
	if strings.HasPrefix(name, previousTmuxPrefix) {
		prefix, suffixParts = previousTmuxPrefix, 3
	}
	parts := strings.Split(strings.TrimPrefix(name, prefix), "-")
	return strings.Join(parts[:len(parts)-suffixParts], "-"), parts[len(parts)-suffixParts], true
}

// sortManagedTmuxSessions orders the list the way someone looking for a session
// scans it: most recently active first, because that is almost always the one
// they walked away from. Name breaks ties so the output is stable.
func sortManagedTmuxSessions(sessions []managedTmuxSession) {
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].activity.Equal(sessions[j].activity) {
			return sessions[i].activity.After(sessions[j].activity)
		}
		return sessions[i].name < sessions[j].name
	})
}

// tmuxIdleLabel renders how long a session has been untouched. Anything under a
// minute reads as "now": a freshly detached session should not look stale.
func tmuxIdleLabel(activity, now time.Time) string {
	idle := now.Sub(activity)
	switch {
	case idle < time.Minute:
		return i18n.T("tmux.idle_now")
	case idle < time.Hour:
		return fmt.Sprintf("%dm", int(idle.Minutes()))
	case idle < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(idle.Hours()), int(idle.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(idle.Hours())/24, int(idle.Hours())%24)
	}
}

// tmuxSessionRows renders the list as aligned columns. Returned as a slice
// rather than printed so both the plain listing and the picker's labels are
// built from one layout — a picker row that does not line up with the listing
// the user just read is its own small confusion.
func tmuxSessionRows(sessions []managedTmuxSession, now time.Time) []string {
	toolWidth, idleWidth, stateWidth := 0, 0, 0
	idles := make([]string, len(sessions))
	states := make([]string, len(sessions))
	for index, session := range sessions {
		idles[index] = tmuxIdleLabel(session.activity, now)
		states[index] = tmuxStateLabel(session)
		if w := style.Width(session.tool); w > toolWidth {
			toolWidth = w
		}
		if w := style.Width(idles[index]); w > idleWidth {
			idleWidth = w
		}
		// State labels differ in length ("exited" vs "detached"), so the column
		// has to be padded too or the workspace after it steps in and out.
		// style.Width counts the tone escapes as zero, so the pad is right.
		if w := style.Width(states[index]); w > stateWidth {
			stateWidth = w
		}
	}
	rows := make([]string, 0, len(sessions))
	for index, session := range sessions {
		rows = append(rows, strings.TrimRight(strings.Join([]string{
			tmuxPad(session.tool, toolWidth),
			tmuxPad(idles[index], idleWidth),
			tmuxPad(states[index], stateWidth),
			session.directory,
		}, "  "), " "))
	}
	return rows
}

func tmuxStateLabel(session managedTmuxSession) string {
	switch {
	case session.dead:
		return style.Color(i18n.T("tmux.state_dead"), style.ToneYellow)
	case session.attached:
		return style.Color(i18n.T("tmux.state_attached"), style.ToneGreen)
	default:
		return style.Dim(i18n.T("tmux.state_detached"))
	}
}

func tmuxPad(value string, width int) string {
	if pad := width - style.Width(value); pad > 0 {
		return value + strings.Repeat(" ", pad)
	}
	return value
}

// tmuxInventoryFormat asks tmux for one line per pane of every session whose
// name could be ours. The managed-pane flag reuses the launch path's own
// predicate, so a session the user created by hand and happened to name
// everyapi-something is never mistaken for a managed one.
func tmuxInventoryFormat() string {
	return strings.Join([]string{
		"#{session_name}",
		"#{session_created}",
		"#{session_activity}",
		"#{session_attached}",
		"#{pane_dead}",
		tmuxManagedPaneFormat(),
		"#{pane_current_path}",
	}, "\t")
}

// parseTmuxInventory turns tmux's output into the session list. A line that
// names a session we do not manage is skipped rather than rejected: the query
// matches on a name glob, so unrelated user sessions legitimately appear in it.
// A malformed line IS an error — that means the format contract broke, and
// silently dropping sessions would make the listing quietly incomplete.
//
// pane_current_path is the last field because a path may contain anything except
// a tab, so it must absorb the remainder of the line.
func parseTmuxInventory(output string) ([]managedTmuxSession, error) {
	var sessions []managedTmuxSession
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 7)
		if len(fields) != 7 {
			return nil, fmt.Errorf("invalid tmux inventory line %q", line)
		}
		name := fields[0]
		managedName := generatedEveryAPITmuxSession(name)
		if !managedName && !legacyEveryAPITmuxSession(name) {
			continue
		}
		// Only the pane EveryAPI started describes the session; a pane the user
		// split off later carries neither our start command nor our state.
		if fields[5] != "1" {
			continue
		}
		// One session, one managed pane. A second would mean the name collided
		// with something we cannot reason about, so keep the first and move on
		// rather than reporting the same session twice.
		if seen[name] {
			continue
		}
		created, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid tmux session creation time %q", fields[1])
		}
		activity, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid tmux session activity time %q", fields[2])
		}
		// session_attached is the NUMBER of clients attached, not a flag — tmux
		// carries a separate session_many_attached precisely because a session
		// open in two terminals reports "2". Insisting on 0/1 here would fail
		// the entire listing the moment one session has a second client.
		attachedClients, err := strconv.Atoi(fields[3])
		if err != nil || attachedClients < 0 {
			return nil, fmt.Errorf("invalid tmux session client count %q", fields[3])
		}
		if fields[4] != "0" && fields[4] != "1" {
			return nil, fmt.Errorf("invalid tmux pane state in %q", line)
		}
		seen[name] = true
		tool, _, _ := tmuxSessionToolAndWorkspace(name)
		if tool == "" {
			tool = i18n.T("tmux.tool_unknown")
		}
		sessions = append(sessions, managedTmuxSession{
			name:      name,
			tool:      tool,
			directory: fields[6],
			created:   time.Unix(created, 0),
			activity:  time.Unix(activity, 0),
			attached:  attachedClients > 0,
			dead:      fields[4] == "1",
			// Every shape that is not the current everyapi-v3- one is pre-v3,
			// including everyapi-v2-: generatedEveryAPITmuxSession still
			// recognizes those, but no launch will ever reuse them.
			legacy: !strings.HasPrefix(name, managedTmuxPrefix),
		})
	}
	sortManagedTmuxSessions(sessions)
	return sessions, nil
}

func managedTmuxSessions() ([]managedTmuxSession, error) {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return nil, errors.New(i18n.T("use.tmux_not_found"))
	}
	output, err := exec.Command(
		tmuxPath,
		"list-panes", "-a",
		"-f", "#{m:everyapi-*,#{session_name}}",
		"-F", tmuxInventoryFormat(),
	).Output()
	if err != nil {
		// tmux exits non-zero with no server running, and "no sessions" is a
		// legitimate answer to this command, not a failure.
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil, nil
		}
		return nil, fmt.Errorf("list tmux sessions: %w", err)
	}
	return parseTmuxInventory(string(output))
}

// runTmuxDefault is what bare `everyapi tmux` does. On a terminal the listing
// alone would just be a table of names to copy by hand, which is the problem
// this command exists to remove — so it lists and then offers to reattach.
func runTmuxDefault() error {
	sessions, err := managedTmuxSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		cliout.Println(i18n.T("tmux.none"))
		return nil
	}
	if !cliprompt.IsInteractive() {
		printTmuxSessions(sessions)
		return nil
	}
	return pickAndAttachTmuxSession(sessions)
}

func runTmuxList(args []string) error {
	fs := flag.NewFlagSet("tmux list", flag.ContinueOnError)
	format := fs.String("format", "human", "output format (human or json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(rest, " "))
	}
	sessions, err := managedTmuxSessions()
	if err != nil {
		return err
	}
	switch *format {
	case "json":
		return printTmuxSessionsJSON(sessions)
	case "human":
		if len(sessions) == 0 {
			cliout.Println(i18n.T("tmux.none"))
			return nil
		}
		printTmuxSessions(sessions)
		return nil
	default:
		return fmt.Errorf("unsupported format %q", *format)
	}
}

func printTmuxSessions(sessions []managedTmuxSession) {
	rows := tmuxSessionRows(sessions, time.Now())
	for index, session := range sessions {
		cliout.Printf("%s\n", rows[index])
		cliout.Printf("  %s\n", style.Dim(tmuxAttachCommandHint(session.name)))
	}
}

// tmuxAttachCommandHint prefers this command over raw tmux: `everyapi tmux
// attach <name>` works the same whether or not the caller is already inside
// tmux, while a bare `tmux attach` refuses to nest.
func tmuxAttachCommandHint(name string) string {
	return "everyapi tmux attach " + name
}

// machineTmuxSession is the --format=json shape. Times are RFC3339 and the idle
// seconds are precomputed, so a consumer does not have to agree with us about
// what "now" is.
type machineTmuxSession struct {
	Name        string `json:"name"`
	Tool        string `json:"tool"`
	Directory   string `json:"directory"`
	Created     string `json:"created"`
	LastActive  string `json:"last_active"`
	IdleSeconds int64  `json:"idle_seconds"`
	Attached    bool   `json:"attached"`
	Dead        bool   `json:"dead"`
	Legacy      bool   `json:"legacy"`
}

func printTmuxSessionsJSON(sessions []managedTmuxSession) error {
	now := time.Now()
	rows := make([]machineTmuxSession, 0, len(sessions))
	for _, session := range sessions {
		rows = append(rows, machineTmuxSession{
			Name:        session.name,
			Tool:        session.tool,
			Directory:   session.directory,
			Created:     session.created.Format(time.RFC3339),
			LastActive:  session.activity.Format(time.RFC3339),
			IdleSeconds: int64(now.Sub(session.activity).Seconds()),
			Attached:    session.attached,
			Dead:        session.dead,
			Legacy:      session.legacy,
		})
	}
	data, err := json.MarshalIndent(map[string]any{"sessions": rows}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tmux sessions: %w", err)
	}
	cliout.Println(string(data))
	return nil
}

func runTmuxAttach(args []string) error {
	// attach takes a bare name rather than flags, so there is no FlagSet to turn
	// -h into usage the way `list` and `kill` get for free. Without this, `attach
	// --help` reports "no managed tmux session named --help".
	for _, arg := range args {
		if arg == "help" || arg == "--help" || arg == "-h" {
			cliout.Println(i18n.T(tmuxUsageKey))
			return nil
		}
	}
	if len(args) > 1 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(args[1:], " "))
	}
	sessions, err := managedTmuxSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		cliout.Println(i18n.T("tmux.none"))
		return nil
	}
	if len(args) == 1 {
		session, err := findTmuxSession(sessions, args[0])
		if err != nil {
			return err
		}
		return attachTmuxSession(session)
	}
	if !cliprompt.IsInteractive() {
		return errors.New(i18n.T("tmux.attach_needs_name"))
	}
	return pickAndAttachTmuxSession(sessions)
}

func findTmuxSession(sessions []managedTmuxSession, name string) (managedTmuxSession, error) {
	for _, session := range sessions {
		if session.name == name {
			return session, nil
		}
	}
	return managedTmuxSession{}, fmt.Errorf(i18n.T("tmux.unknown_session"), name)
}

func pickAndAttachTmuxSession(sessions []managedTmuxSession) error {
	rows := tmuxSessionRows(sessions, time.Now())
	index, err := cliprompt.Pick(i18n.T("tmux.pick_prompt"), rows)
	if err != nil {
		return err
	}
	return attachTmuxSession(sessions[index])
}

// tmuxAttachAction picks how to reach a session from where the caller is
// standing. Inside tmux the client is already attached to something, so the move
// is a client switch — attach-session refuses to nest, which is exactly the
// failure a user hits when they copy a raw `tmux attach` out of the launch
// banner while still inside tmux. Outside tmux there is no client yet, so this
// process becomes one.
func tmuxAttachAction(tmuxEnvironment string) string {
	if tmuxEnvironment != "" {
		return "switch-client"
	}
	return "attach-session"
}

// attachTmuxSession hands this terminal to the session. A dead session is
// refused up front: attaching to it shows a frozen pane and no way to act, which
// reads as a hang rather than as "the tool already exited".
func attachTmuxSession(session managedTmuxSession) error {
	if session.dead {
		// The locale string names the session twice: once as the subject, once
		// inside the `everyapi tmux kill <name>` hint.
		return fmt.Errorf(i18n.T("tmux.attach_dead"), session.name, session.name)
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New(i18n.T("use.tmux_not_found"))
	}
	target := exactTmuxSessionTarget(session.name)
	command := exec.Command(tmuxPath, tmuxAttachAction(os.Getenv("TMUX")), "-t", target)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		// The session can end between the listing and this call — a tool that was
		// on its last breath, or another terminal killing it. Report that as the
		// race it is instead of as tmux's exit status.
		if exec.Command(tmuxPath, "has-session", "-t", target).Run() != nil {
			return fmt.Errorf(i18n.T("tmux.vanished"), session.name)
		}
		return fmt.Errorf("attach to tmux session %s: %w", session.name, err)
	}
	return nil
}

// tmuxKillAuthorization decides whether the caller has already authorized this
// kill, still has to be asked, or must be turned away. Killing a session kills
// the agent running in it, and a detached agent may well be mid-task.
//
// Without a terminal there is nobody to ask, and for --all that is not a reason
// to go ahead: cliprompt.IsInteractive needs BOTH stdin and stdout to be
// terminals, so `everyapi tmux kill --all | tee log` typed at a real prompt lands
// here and going ahead would mass-kill every agent in silence. Explicitly named
// sessions are already a statement of intent, so those pass through.
//
// Kept separate from runTmuxKill so it can be tested without executing anything:
// a test that reaches the kill itself destroys real sessions.
func tmuxKillAuthorization(yes, all, interactive bool) (prompt bool, err error) {
	if yes {
		return false, nil
	}
	if interactive {
		return true, nil
	}
	if all {
		return false, errors.New(i18n.T("tmux.kill_all_needs_confirmation"))
	}
	return false, nil
}

func runTmuxKill(args []string) error {
	// The confirm flag goes through cliprompt so this command accepts the same
	// spellings as every other destructive one (-y / --y / -yes / --yes); its
	// doc explains why a FlagSet cannot do it — stdlib flag.Parse stops at the
	// first non-flag token, so `kill <session> -y` would leave -y unparsed.
	yes, rest := cliprompt.SplitConfirmFlag(args)
	// --all has the same positional problem. Managed session names always start
	// with "everyapi-", so nothing positional is dash-shaped: hand the parser
	// only the dash-shaped tokens and keep the rest as names. Parsing them
	// separately is what makes `kill <name> --all` report the conflict instead of
	// hunting for a session called "--all".
	var flags, names []string
	for _, arg := range rest {
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			continue
		}
		names = append(names, arg)
	}
	fs := flag.NewFlagSet("tmux kill", flag.ContinueOnError)
	all := fs.Bool("all", false, "kill every managed session")
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if *all && len(names) > 0 {
		return errors.New(i18n.T("tmux.kill_all_with_name"))
	}
	if !*all && len(names) == 0 {
		return errors.New(i18n.T("tmux.kill_needs_target"))
	}
	sessions, err := managedTmuxSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		cliout.Println(i18n.T("tmux.none"))
		return nil
	}
	targets := sessions
	if !*all {
		targets = nil
		for _, name := range names {
			session, err := findTmuxSession(sessions, name)
			if err != nil {
				return err
			}
			targets = append(targets, session)
		}
	}
	prompt, err := tmuxKillAuthorization(yes, *all, cliprompt.IsInteractive())
	if err != nil {
		return err
	}
	if prompt {
		rows := tmuxSessionRows(targets, time.Now())
		for _, row := range rows {
			cliout.Printf("  %s\n", row)
		}
		confirmed, err := cliprompt.YesNo(bufio.NewReader(os.Stdin), fmt.Sprintf(i18n.T("tmux.kill_confirm"), len(targets)), false)
		if err != nil {
			return err
		}
		if !confirmed {
			cliout.Println(i18n.T("tmux.kill_cancelled"))
			return nil
		}
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New(i18n.T("use.tmux_not_found"))
	}
	killed := 0
	for _, session := range targets {
		// Exact-name target: a bare name is a tmux pattern, and killing by
		// pattern could take a session whose name merely starts with this one.
		if err := exec.Command(tmuxPath, "kill-session", "-t", exactTmuxSessionTarget(session.name)).Run(); err != nil {
			// A session that ended on its own between the listing and here needs
			// no report: the caller asked for it to be gone and it is.
			if exec.Command(tmuxPath, "has-session", "-t", exactTmuxSessionTarget(session.name)).Run() != nil {
				continue
			}
			return fmt.Errorf("kill tmux session %s: %w", session.name, err)
		}
		killed++
	}
	cliout.Printf(i18n.T("tmux.killed")+"\n", killed)
	return nil
}
