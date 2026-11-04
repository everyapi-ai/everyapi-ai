package cmd

// `everyapi clean` deletes local agent session history. Unlike `sessions list`, which projects anything JSON-shaped under a provider's root and is free to be generous because it only reads, this command removes data, so it recognizes a session only by the exact on-disk shape its tool writes, and it deletes through the tool itself wherever the tool keeps an index of its own.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/procstate"
)

// agentSessionActiveWindow protects sessions that may still be running. Claude Code, Codex and Pi append to the transcript on every turn, so a transcript written this recently most likely belongs to an agent that is open right now (including the one that may be invoking this command), and deleting it would silently drop everything the agent writes afterwards. It is the fallback signal: an open but idle session falls outside it, which is why Claude Code sessions are also checked against the processes Claude Code registers (liveClaudeSessionIDs). Codex and Pi register nothing comparable.
const agentSessionActiveWindow = 10 * time.Minute

// cleanTargets are the tools `everyapi clean` accepts, in display order. "all" expands to every one of them.
var cleanTargets = []string{"claude", "codex", "pi"}

// cleanTargetAliases maps the other names users reach for onto a target. Codex ships inside the ChatGPT desktop app, so "chatgpt" is what many people call its history.
var cleanTargetAliases = map[string]string{"chatgpt": "codex"}

const codexDeleteTimeout = time.Minute

// codexDeleteUsagePattern is the usage line of `codex delete --help`. An older Codex prints its top-level help instead, whose usage line names no subcommand.
var codexDeleteUsagePattern = regexp.MustCompile(`Usage: codex(\.exe)? delete `)

// piSessionFilePattern is the name Pi gives a session transcript: the creation time with ':' and '.' replaced by '-', an underscore, and the session id.
var piSessionFilePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z_[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}\.jsonl$`)

// codexRolloutFilePattern is a Codex session file: `rollout-<timestamp>-<session uuid>.jsonl`, or the zstd copy Codex keeps beside it.
var codexRolloutFilePattern = regexp.MustCompile(`^rollout-.+-([[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12})\.jsonl(\.zst)?$`)

const cleanUsage = `usage: everyapi clean <claude|codex|pi|all>... [--older-than AGE] [--dry-run] [--yes] [--format=json]

Deletes the session history agent tools keep on this machine.

  claude             Claude Code sessions, with their subagent transcripts, tool results and file checkpoints
  codex              Codex sessions, active and archived (alias: chatgpt), deleted through ` + "`codex delete`" + `
  pi                 Pi sessions, with their subagent runs
  all                all of the above

  --older-than AGE   only sessions last written more than AGE ago (e.g. 30d, 2w, 12h)
  --dry-run          list what would be deleted without deleting anything
  --yes, -y          skip the confirmation prompt (required when not on a terminal)
  --format FORMAT    text (default) or json

Sessions open in a running Claude Code, and sessions of any tool written in the last 10 minutes, are always kept. Claude Code project memory and prompt history are kept.`

// agentSessionCleanRoots are the directories clean reads. A struct rather than a home path so tests can point each tool somewhere else.
type agentSessionCleanRoots struct {
	claudeHome string
	codexHome  string
	piSessions string
}

func defaultAgentSessionCleanRoots(home string) agentSessionCleanRoots {
	// Same CODEX_HOME rule as `sessions list`, and the same environment the `codex delete` child inherits, so both look at one home.
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" || !filepath.IsAbs(codexHome) {
		codexHome = filepath.Join(home, ".codex")
	}
	return agentSessionCleanRoots{
		claudeHome: filepath.Join(home, ".claude"),
		codexHome:  codexHome,
		piSessions: filepath.Join(home, ".pi", "agent", "sessions"),
	}
}

type cleanableAgentSession struct {
	Provider   string  `json:"provider"`
	SessionID  string  `json:"session_id"`
	Title      string  `json:"title"`
	CWD        *string `json:"cwd"`
	FilePath   string  `json:"file_path"`
	ModifiedAt string  `json:"modified_at"`
	// SizeBytes covers every file the session owns, so it is what deleting the session frees.
	SizeBytes int64 `json:"size_bytes"`
	// files are the session's own records. Claude Code and Pi sessions are removed file by file, companions first: an interrupted run then leaves the transcript in place, and the next run finds the session again and finishes the job. Codex sessions are handed to `codex delete` instead, and files only serve the pre-delete activity check.
	files    []string
	modified time.Time
}

type agentSessionCleanFailure struct {
	FilePath string `json:"file_path"`
	Error    string `json:"error"`
}

type agentSessionCleanReport struct {
	DryRun        bool                       `json:"dry_run"`
	Providers     []string                   `json:"providers"`
	Sessions      []cleanableAgentSession    `json:"sessions"`
	TotalBytes    int64                      `json:"total_bytes"`
	Removed       int                        `json:"removed"`
	FreedBytes    int64                      `json:"freed_bytes"`
	SkippedActive int                        `json:"skipped_active"`
	Failed        []agentSessionCleanFailure `json:"failed"`
}

// codexDeleteSession deletes one Codex session through Codex's own CLI, which removes the rollout files and the thread's row in Codex's state database together; deleting the files directly would leave threads Codex lists but cannot open. --force is what Codex requires to delete without a terminal, and it accepts it only with a session UUID, which is all this passes. The child is pinned to codexHome, the home the session was discovered in: an inherited relative CODEX_HOME is resolved by Codex against the working directory, while discovery falls back to ~/.codex for it. A variable so tests can stand in for Codex.
var codexDeleteSession = func(codexPath, codexHome, sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), codexDeleteTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, codexPath, "delete", "--force", sessionID)
	command.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	output, err := command.CombinedOutput()
	if err != nil {
		if message := strings.TrimSpace(string(output)); message != "" {
			return fmt.Errorf("codex delete: %s", message)
		}
		return fmt.Errorf("codex delete: %w", err)
	}
	return nil
}

// findCodexForDelete locates a Codex CLI that has the `delete` command (Codex 0.166 and later). An older Codex would read `delete` as a prompt for a new interactive session, so its absence has to be established before anything is run, from the subcommand's own help.
var findCodexForDelete = func() (string, error) {
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return "", errors.New("cannot clean Codex sessions: the codex CLI is not on PATH, and Codex sessions are deleted through `codex delete` so Codex's own index stays consistent — install or update Codex, or clean the other tools only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexDeleteTimeout)
	defer cancel()
	help, err := exec.CommandContext(ctx, codexPath, "delete", "--help").CombinedOutput()
	if err != nil || !codexDeleteUsagePattern.Match(help) {
		return "", errors.New("cannot clean Codex sessions: this Codex CLI has no `delete` command — run `codex update` and try again")
	}
	return codexPath, nil
}

// Clean dispatches `everyapi clean`. It is local and login-free: it only ever touches this machine.
func Clean(args []string) error {
	for _, arg := range args {
		if arg == "help" || arg == "--help" || arg == "-h" {
			cliout.Println(cleanUsage)
			return nil
		}
	}
	yes, rest := cliprompt.SplitConfirmFlag(args)
	flags := flag.NewFlagSet("everyapi clean", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	olderThan := flags.String("older-than", "", "only sessions last written more than this long ago")
	dryRun := flags.Bool("dry-run", false, "list without deleting")
	format := flags.String("format", "text", "output format (json or text)")
	// flag.Parse stops at the first positional, so targets and flags are collected in turns: `clean claude --dry-run codex` reads the same as `clean --dry-run claude codex`.
	var names []string
	for {
		if err := flags.Parse(rest); err != nil {
			return err
		}
		if flags.NArg() == 0 {
			break
		}
		names = append(names, flags.Arg(0))
		rest = flags.Args()[1:]
	}
	providers, err := parseCleanTargets(names)
	if err != nil {
		return err
	}
	var minimumAge time.Duration
	if *olderThan != "" {
		if minimumAge, err = parseSessionAge(*olderThan); err != nil {
			return err
		}
	}
	if *format != "json" && *format != "text" {
		return fmt.Errorf("unsupported clean format %q — use json or text", *format)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	roots := defaultAgentSessionCleanRoots(home)
	discovered, err := discoverCleanableAgentSessions(roots, providers)
	if err != nil {
		return err
	}
	selected, skippedActive := selectCleanableAgentSessions(discovered, time.Now(), minimumAge, liveClaudeSessionIDs(roots.claudeHome))
	// Titles cost a read and a parse of every transcript, so they are projected only for the outputs that show them: the dry-run listing and JSON.
	if *dryRun || *format == "json" {
		projectCleanableTitles(selected)
	}
	report := agentSessionCleanReport{DryRun: *dryRun, Providers: providers, Sessions: selected, SkippedActive: skippedActive, Failed: []agentSessionCleanFailure{}}
	for _, session := range selected {
		report.TotalBytes += session.SizeBytes
	}
	if *dryRun || len(selected) == 0 {
		return printAgentSessionCleanReport(report, *format)
	}
	// Resolve Codex before deleting anything, so a missing or outdated Codex fails the whole run up front instead of after the other tools' sessions are already gone.
	codexPath := ""
	if countCleanableSessions(selected, "codex") > 0 {
		if codexPath, err = findCodexForDelete(); err != nil {
			return err
		}
	}
	// JSON output is for a program on the other end, and a prompt would land in the middle of the document it parses; without a terminal there is nobody to answer at all. Either way a bulk delete needs --yes, the same rule `tmux kill --all` follows.
	if !yes {
		if *format == "json" || !cliprompt.IsInteractive() {
			return fmt.Errorf("refusing to delete %d local sessions without confirmation — rerun with --yes, or with --dry-run to see them first", len(selected))
		}
		printCleanSummary(selected)
		confirmed, err := cliprompt.YesNo(bufio.NewReader(os.Stdin), fmt.Sprintf("Delete these %d sessions (%s)? This cannot be undone", len(selected), humanSessionBytes(report.TotalBytes)), false)
		if err != nil {
			return err
		}
		if !confirmed {
			cliout.Println("Cancelled; nothing was deleted.")
			return nil
		}
	}
	if codexCount := countCleanableSessions(selected, "codex"); codexCount > 0 && *format == "text" {
		cliout.Printf("Deleting %d Codex sessions through `codex delete`…\n", codexCount)
	}
	// Read again after the prompt, which can stay open for as long as the user likes: a Claude Code session opened in the meantime is live and must survive.
	liveClaude := liveClaudeSessionIDs(roots.claudeHome)
	for _, session := range selected {
		removed, err := removeCleanableAgentSession(session, codexPath, roots.codexHome, liveClaude)
		switch {
		case err != nil:
			report.Failed = append(report.Failed, agentSessionCleanFailure{FilePath: session.FilePath, Error: err.Error()})
		case !removed:
			report.SkippedActive++
		default:
			report.Removed++
			report.FreedBytes += session.SizeBytes
		}
	}
	if err := printAgentSessionCleanReport(report, *format); err != nil {
		return err
	}
	if len(report.Failed) > 0 {
		return fmt.Errorf("failed to delete %d of %d sessions; first failure: %s: %s", len(report.Failed), len(selected), report.Failed[0].FilePath, report.Failed[0].Error)
	}
	return nil
}

// parseCleanTargets resolves the named tools. Naming nothing is an error rather than "everything": wiping every tool's history should take the word `all`.
func parseCleanTargets(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("name what to clean: %s or all — try `everyapi clean help`", strings.Join(cleanTargets, ", "))
	}
	requested := make(map[string]bool)
	for _, name := range names {
		target := strings.ToLower(strings.TrimSpace(name))
		if alias, ok := cleanTargetAliases[target]; ok {
			target = alias
		}
		switch {
		case target == "all":
			for _, each := range cleanTargets {
				requested[each] = true
			}
		case slices.Contains(cleanTargets, target):
			requested[target] = true
		default:
			return nil, fmt.Errorf("cannot clean %q — choose from %s, chatgpt (Codex) or all", name, strings.Join(cleanTargets, ", "))
		}
	}
	providers := make([]string, 0, len(requested))
	for _, target := range cleanTargets {
		if requested[target] {
			providers = append(providers, target)
		}
	}
	return providers, nil
}

// parseSessionAge accepts Go durations (12h, 90m) plus whole days and weeks, which is how session age is naturally expressed and which time.ParseDuration does not understand.
func parseSessionAge(value string) (time.Duration, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errors.New("--older-than needs a value such as 30d, 2w or 12h")
	}
	var age time.Duration
	unit := map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour}
	if multiplier, ok := unit[trimmed[len(trimmed)-1]]; ok && len(trimmed) > 1 {
		count, err := strconv.Atoi(trimmed[:len(trimmed)-1])
		// A count past what time.Duration holds would wrap around rather than fail, and a wrapped age could select sessions far newer than the user asked for.
		if err != nil || int64(count) > math.MaxInt64/int64(multiplier) {
			return 0, fmt.Errorf("invalid --older-than %q — use a number of days or weeks (30d, 2w) or a duration (12h)", value)
		}
		age = time.Duration(count) * multiplier
	} else {
		parsed, err := time.ParseDuration(trimmed)
		if err != nil {
			return 0, fmt.Errorf("invalid --older-than %q — use a number of days or weeks (30d, 2w) or a duration (12h)", value)
		}
		age = parsed
	}
	if age <= 0 {
		return 0, fmt.Errorf("--older-than must be positive, got %q", value)
	}
	return age, nil
}

func countCleanableSessions(sessions []cleanableAgentSession, provider string) int {
	count := 0
	for _, session := range sessions {
		if session.Provider == provider {
			count++
		}
	}
	return count
}

func discoverCleanableAgentSessions(roots agentSessionCleanRoots, providers []string) ([]cleanableAgentSession, error) {
	var sessions []cleanableAgentSession
	for _, provider := range providers {
		var found []cleanableAgentSession
		var err error
		switch provider {
		case "claude":
			found, err = discoverClaudeCleanableSessions(roots.claudeHome)
		case "codex":
			found, err = discoverCodexCleanableSessions(roots.codexHome)
		case "pi":
			found, err = discoverPiCleanableSessions(roots.piSessions)
		}
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, found...)
	}
	// Oldest first: that is the order someone deciding what to drop reads in, and it keeps the listing stable between a dry run and the real one.
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].modified.Equal(sessions[j].modified) {
			return sessions[i].modified.Before(sessions[j].modified)
		}
		return sessions[i].FilePath < sessions[j].FilePath
	})
	return sessions, nil
}

// discoverClaudeCleanableSessions finds `<claudeHome>/projects/<project>/<uuid>.jsonl`. Everything else in a project directory — memory/, files other tools drop there — is not a session and is never touched. The per-session data Claude Code keeps elsewhere under the same id goes with the transcript: subagent transcripts and tool results in `<project>/<uuid>/`, the recovery marker `everyapi use` writes, and the file-edit checkpoints and environment snapshots under file-history/ and session-env/.
func discoverClaudeCleanableSessions(claudeHome string) ([]cleanableAgentSession, error) {
	projectsRoot := filepath.Join(claudeHome, "projects")
	projects, err := readSessionDir(projectsRoot)
	if err != nil {
		return nil, err
	}
	var sessions []cleanableAgentSession
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		dir := filepath.Join(projectsRoot, project.Name())
		entries, err := readSessionDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			id, isJSONL := strings.CutSuffix(entry.Name(), ".jsonl")
			if !isJSONL || !entry.Type().IsRegular() || !claudeSessionIDPattern.MatchString(id) {
				continue
			}
			session, ok := newCleanableAgentSession("claude", filepath.Join(dir, entry.Name()), []string{
				filepath.Join(dir, id),
				claudeRecoveryMarkerPath(dir, id),
				filepath.Join(claudeHome, "file-history", id),
				filepath.Join(claudeHome, "session-env", id),
			})
			if ok {
				sessions = append(sessions, session)
			}
		}
	}
	return sessions, nil
}

// discoverCodexCleanableSessions finds Codex rollouts under sessions/YYYY/MM/DD/ and archived_sessions/. A session may be stored as `.jsonl`, as the `.jsonl.zst` copy Codex keeps beside it, or both; they are grouped by the session id in the name, which is also what `codex delete` takes.
func discoverCodexCleanableSessions(codexHome string) ([]cleanableAgentSession, error) {
	byID := make(map[string]*cleanableAgentSession)
	var order []string
	for _, root := range []struct {
		path  string
		depth int
	}{{filepath.Join(codexHome, "sessions"), 3}, {filepath.Join(codexHome, "archived_sessions"), 0}} {
		files, err := codexRolloutFiles(root.path, root.depth)
		if err != nil {
			return nil, err
		}
		for _, path := range files {
			match := codexRolloutFilePattern.FindStringSubmatch(filepath.Base(path))
			info, err := os.Lstat(path)
			if err != nil {
				continue
			}
			id := strings.ToLower(match[1])
			session, ok := byID[id]
			if !ok {
				session = &cleanableAgentSession{Provider: "codex", SessionID: id, Title: id}
				byID[id] = session
				order = append(order, id)
			}
			session.files = append(session.files, path)
			session.SizeBytes += info.Size()
			if info.ModTime().After(session.modified) {
				session.modified = info.ModTime()
			}
			// The plain transcript is the one worth naming and the only one that can be read for a title.
			if session.FilePath == "" || match[2] == "" {
				session.FilePath = path
			}
		}
	}
	sessions := make([]cleanableAgentSession, 0, len(order))
	for _, id := range order {
		session := byID[id]
		session.ModifiedAt = session.modified.UTC().Format(time.RFC3339)
		sessions = append(sessions, *session)
	}
	return sessions, nil
}

// codexRolloutFiles lists rollout files under root, descending at most maxDepth directories and never through a symbolic link.
func codexRolloutFiles(root string, maxDepth int) ([]string, error) {
	var files []string
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		entries, err := readSessionDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			switch {
			case entry.IsDir():
				if depth < maxDepth {
					if err := walk(path, depth+1); err != nil {
						return err
					}
				}
			case entry.Type().IsRegular() && codexRolloutFilePattern.MatchString(entry.Name()):
				files = append(files, path)
			}
		}
		return nil
	}
	return files, walk(root, 0)
}

// discoverPiCleanableSessions finds Pi transcripts at the sessions root and one level down, in the per-working-directory folders Pi creates. A transcript's subagent runs live in a folder with the transcript's own name. Anything else, such as subagent-artifacts/, is left alone.
func discoverPiCleanableSessions(root string) ([]cleanableAgentSession, error) {
	entries, err := readSessionDir(root)
	if err != nil {
		return nil, err
	}
	dirs := []string{root}
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(root, entry.Name()))
		}
	}
	var sessions []cleanableAgentSession
	for _, dir := range dirs {
		files := entries
		if dir != root {
			if files, err = readSessionDir(dir); err != nil {
				return nil, err
			}
		}
		for _, file := range files {
			if !file.Type().IsRegular() || !piSessionFilePattern.MatchString(file.Name()) {
				continue
			}
			session, ok := newCleanableAgentSession("pi", filepath.Join(dir, file.Name()), []string{
				filepath.Join(dir, strings.TrimSuffix(file.Name(), ".jsonl")),
			})
			if ok {
				sessions = append(sessions, session)
			}
		}
	}
	return sessions, nil
}

// readSessionDir treats a missing directory as empty (the tool is simply not installed) and an unreadable one the way `sessions list` does, as nothing to see.
func readSessionDir(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agent session directory failed: %w", err)
	}
	return entries, nil
}

// newCleanableAgentSession builds a file-deleted session: the transcript at path plus whichever companion candidates exist. The transcript goes last in files so it is removed last.
func newCleanableAgentSession(provider, path string, companionCandidates []string) (cleanableAgentSession, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return cleanableAgentSession{}, false
	}
	session := cleanableAgentSession{
		Provider:   provider,
		FilePath:   path,
		ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
		SizeBytes:  info.Size(),
		modified:   info.ModTime(),
	}
	for _, candidate := range companionCandidates {
		if _, err := os.Lstat(candidate); err != nil {
			continue
		}
		session.files = append(session.files, candidate)
		session.SizeBytes += sessionPathSize(candidate)
	}
	session.files = append(session.files, path)
	// The id stays the file name's: it is what every companion path is keyed by, while an id read from the transcript can name another session, such as the one a fork or an `everyapi use` recovery copied its records from.
	session.SessionID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	session.Title = session.SessionID
	return session, true
}

// projectCleanableTitles fills in each session's title and working directory from its plain transcript. A Codex session stored only as .jsonl.zst cannot be read and keeps its id as the title.
func projectCleanableTitles(sessions []cleanableAgentSession) {
	for index := range sessions {
		session := &sessions[index]
		if !strings.HasSuffix(session.FilePath, ".jsonl") {
			continue
		}
		if projected, ok := projectAgentCandidate(agentCandidate{provider: session.Provider, path: session.FilePath}); ok {
			session.Title, session.CWD = projected.Title, projected.CWD
		}
	}
}

// sessionPathSize totals the regular files under path. WalkDir does not follow symbolic links, so a link inside a companion is counted as itself, which is also all RemoveAll deletes.
func sessionPathSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// selectCleanableAgentSessions keeps the sessions at least minimumAge old (every session when minimumAge is zero). It reports separately how many old-enough sessions it held back because they are open in a running Claude Code or were written inside agentSessionActiveWindow, so the caller can explain why not everything was deleted.
func selectCleanableAgentSessions(sessions []cleanableAgentSession, now time.Time, minimumAge time.Duration, liveClaude map[string]bool) (selected []cleanableAgentSession, skippedActive int) {
	selected = []cleanableAgentSession{}
	for _, session := range sessions {
		age := now.Sub(session.modified)
		if age < minimumAge {
			continue
		}
		if age < agentSessionActiveWindow || claudeSessionOpen(session, liveClaude) {
			skippedActive++
			continue
		}
		selected = append(selected, session)
	}
	return selected, skippedActive
}

// removeCleanableAgentSession deletes one session. It first re-reads the session's files: the confirmation prompt can sit open for as long as the user likes, and a session resumed in the meantime is live again and must survive. removed is false when that check held the session back.
func removeCleanableAgentSession(session cleanableAgentSession, codexPath, codexHome string, liveClaude map[string]bool) (removed bool, err error) {
	if claudeSessionOpen(session, liveClaude) || sessionWrittenSince(session) {
		return false, nil
	}
	if session.Provider == "codex" {
		if !anyPathExists(session.files) {
			return true, nil
		}
		return true, codexDeleteSession(codexPath, codexHome, session.SessionID)
	}
	for _, path := range session.files {
		if err := os.RemoveAll(path); err != nil {
			return false, err
		}
	}
	return true, nil
}

// liveClaudeSessionIDs returns the sessions open in a running Claude Code. Every Claude Code process registers itself in <claudeHome>/sessions/<pid>.json with the id of the session it has open, and a session can sit idle there for hours: the write window alone would take an agent left waiting in a detached `everyapi use` tmux session for an abandoned one, and deleting its transcript under it loses everything before its next turn. A registry entry whose process is gone is stale and protects nothing; procstate.Alive errs towards "alive", which here only means a session is kept.
func liveClaudeSessionIDs(claudeHome string) map[string]bool {
	live := make(map[string]bool)
	entries, _ := readSessionDir(filepath.Join(claudeHome, "sessions"))
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(claudeHome, "sessions", entry.Name()))
		if err != nil {
			continue
		}
		var registration struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(data, &registration) != nil || registration.SessionID == "" {
			continue
		}
		if procstate.Alive(registration.PID) {
			live[registration.SessionID] = true
		}
	}
	return live
}

func claudeSessionOpen(session cleanableAgentSession, liveClaude map[string]bool) bool {
	return session.Provider == "claude" && liveClaude[session.SessionID]
}

func anyPathExists(paths []string) bool {
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			return true
		}
	}
	return false
}

// sessionWrittenSince reports whether any file the session had at discovery time has been modified since, or is now inside the active window. A file that has disappeared is not activity.
func sessionWrittenSince(session cleanableAgentSession) bool {
	transcripts := session.files
	if session.Provider != "codex" {
		// Only the transcript is appended to; companion directories change for unrelated reasons.
		transcripts = []string{session.FilePath}
	}
	for _, path := range transcripts {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(session.modified) || time.Since(info.ModTime()) < agentSessionActiveWindow {
			return true
		}
	}
	return false
}

func printCleanableAgentSessions(sessions []cleanableAgentSession) {
	for _, session := range sessions {
		cliout.Printf("  %-6s  %s  %9s  %s\n", session.Provider, session.modified.Local().Format("2006-01-02"), humanSessionBytes(session.SizeBytes), session.Title)
	}
}

// printCleanSummary is what the confirmation prompt shows: one line per tool. A full listing of hundreds of sessions would scroll the question itself off screen; --dry-run is where the full list lives.
func printCleanSummary(sessions []cleanableAgentSession) {
	for _, provider := range cleanTargets {
		count := 0
		var size int64
		var oldest, newest time.Time
		for _, session := range sessions {
			if session.Provider != provider {
				continue
			}
			if count == 0 || session.modified.Before(oldest) {
				oldest = session.modified
			}
			if count == 0 || session.modified.After(newest) {
				newest = session.modified
			}
			count++
			size += session.SizeBytes
		}
		if count > 0 {
			cliout.Printf("  %-6s  %4d sessions  %9s  %s – %s\n", provider, count, humanSessionBytes(size), oldest.Local().Format("2006-01-02"), newest.Local().Format("2006-01-02"))
		}
	}
	cliout.Println("  (run with --dry-run to list every session)")
}

func printAgentSessionCleanReport(report agentSessionCleanReport, format string) error {
	if format == "json" {
		encoded, err := json.Marshal(report)
		if err != nil {
			return err
		}
		cliout.Println(string(encoded))
		return nil
	}
	switch {
	case len(report.Sessions) == 0:
		cliout.Printf("No %s sessions to clean.\n", strings.Join(report.Providers, " or "))
	case report.DryRun:
		printCleanableAgentSessions(report.Sessions)
		cliout.Printf("Would delete %d sessions (%s). Nothing was deleted (--dry-run).\n", len(report.Sessions), humanSessionBytes(report.TotalBytes))
	default:
		cliout.Printf("Deleted %d sessions, freed %s.\n", report.Removed, humanSessionBytes(report.FreedBytes))
	}
	if report.SkippedActive > 0 {
		cliout.Printf("Kept %d sessions that are open in a running Claude Code or were written in the last %d minutes.\n", report.SkippedActive, int(agentSessionActiveWindow.Minutes()))
	}
	return nil
}

func humanSessionBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}
