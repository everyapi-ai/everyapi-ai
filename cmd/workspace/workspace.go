// Package workspace implements local workspace automation for the CLI.
//
// The package deliberately talks to the operating system and the repository in the current process. It does not discover or invoke another product's executable. Page automation uses persisted local state, while mobile automation uses installed bridges or the local device registry.
package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type result struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Command returns a native handler for one CLI command family.
func Command(name string) func([]string) error {
	return func(args []string) error { return run(name, args) }
}

func run(name string, args []string) error {
	var data any
	var err error
	if hasFlag(args, "--help") || hasFlag(args, "-h") || hasHelpWord(args) {
		data, err = commandHelp(name)
	} else {
		dispatchArgs := stripRoutingFlags(name, stripGlobalFlags(stripFlag(args, "--json")))
		if name == "environment" {
			dispatchArgs = preserveFlag(args, dispatchArgs, "pairing-code")
		}
		// serve uses --json as its non-interactive/background mode switch. Keep
		// that marker for the handler; all other native handlers only need the
		// wrapper-level output flag removed from their argument list.
		if name == "serve" && hasFlag(args, "--json") {
			dispatchArgs = append(dispatchArgs, "--json")
		}
		switch name {
		case "open":
			data, err = openRuntime(dispatchArgs)
		case "serve":
			data, err = serve(dispatchArgs)
		case "status":
			data, err = status(dispatchArgs)
		case "host":
			data, err = host(dispatchArgs)
		case "claude-teams":
			data, err = claudeTeams(dispatchArgs)
		case "repo":
			data, err = repo(dispatchArgs)
		case "worktree":
			data, err = worktree(dispatchArgs)
		case "terminal":
			data, err = terminal(dispatchArgs)
		case "exec":
			data, err = browserExec(dispatchArgs)
		case "skills":
			data, err = skills(dispatchArgs)
		case "account":
			data, err = managedAccounts(dispatchArgs)
		case "file":
			data, err = files(dispatchArgs)
		case "diagnostics":
			data, err = diagnostics(dispatchArgs)
		case "agent-context":
			data, err = agentContext(dispatchArgs)
		case "orchestration":
			data, err = orchestration(dispatchArgs)
		case "automations":
			data, err = automations(dispatchArgs)
		case "environment":
			data, err = environment(dispatchArgs)
		case "project":
			data, err = project(dispatchArgs)
		case "vm":
			data, err = vm(dispatchArgs)
		case "emulator":
			data, err = emulator(dispatchArgs)
		case "linear":
			data, err = localLinear(dispatchArgs)
		case "snapshot", "goto", "find", "get", "screenshot", "full-screenshot", "pdf", "click", "fill", "type", "select", "scroll", "back", "reload", "eval", "wait", "check", "uncheck", "focus", "clear", "select-all", "keypress", "hover", "drag", "upload", "scrollintoview", "dblclick", "forward", "is":
			data, err = browser(dispatchArgs, name)
		case "tab":
			data, err = tabs(dispatchArgs)
		case "cookie", "storage", "console", "network", "clipboard", "dialog", "download", "highlight", "capture", "viewport", "geolocation", "intercept", "mouse", "inserttext", "set":
			data, err = browserAux(dispatchArgs, name)
		case "agent":
			data, err = agent(dispatchArgs)
		default:
			err = unsupported(name)
		}
	}
	jsonOutput := hasFlag(args, "--json")
	if jsonOutput {
		response := result{OK: err == nil, Command: name, Data: data}
		if err != nil {
			response.Error = err.Error()
		}
		if encodeErr := json.NewEncoder(os.Stdout).Encode(response); encodeErr != nil {
			return encodeErr
		}
		return err
	}
	if err != nil {
		return err
	}
	return printData(data)
}

var nativeUsages = map[string]string{
	"open":            "Usage: everyapi open [--url URL] [--port PORT] [--json]",
	"serve":           "Usage: everyapi serve [--project-root DIR] [--port PORT] [--background] [--json]",
	"claude-teams":    "Usage: everyapi claude-teams [--command CMD] [args...]",
	"status":          "Usage: everyapi status [--json]",
	"host":            "Usage: everyapi host list [--json]",
	"repo":            "Usage: everyapi repo <list|show|add|set-base-ref|search-refs> [flags]",
	"worktree":        "Usage: everyapi worktree <list|current|show|create|set|rm|ps> [flags]",
	"terminal":        "Usage: everyapi terminal <list|show|read|send|wait|stop|create|rename|split|switch|focus|close> [flags]",
	"snapshot":        "Usage: everyapi snapshot [--json]",
	"goto":            "Usage: everyapi goto --url URL [--json]",
	"find":            "Usage: everyapi find <text> [--json]",
	"get":             "Usage: everyapi get [--json]",
	"screenshot":      "Usage: everyapi screenshot [--out FILE] [--json]",
	"click":           "Usage: everyapi click --element SELECTOR [--json]",
	"fill":            "Usage: everyapi fill --element SELECTOR --value VALUE [--json]",
	"type":            "Usage: everyapi type --element SELECTOR --value VALUE [--json]",
	"select":          "Usage: everyapi select --element SELECTOR --value VALUE [--json]",
	"scroll":          "Usage: everyapi scroll [--amount N] [--direction up|down] [--json]",
	"back":            "Usage: everyapi back [--json]",
	"reload":          "Usage: everyapi reload [--json]",
	"eval":            "Usage: everyapi eval --expression EXPR [--json]",
	"wait":            "Usage: everyapi wait --text TEXT [--timeout SECONDS] [--json]",
	"check":           "Usage: everyapi check --element SELECTOR [--json]",
	"uncheck":         "Usage: everyapi uncheck --element SELECTOR [--json]",
	"focus":           "Usage: everyapi focus --element SELECTOR [--json]",
	"clear":           "Usage: everyapi clear --element SELECTOR [--json]",
	"select-all":      "Usage: everyapi select-all [--json]",
	"keypress":        "Usage: everyapi keypress --key KEY [--json]",
	"pdf":             "Usage: everyapi pdf [--out FILE] [--json]",
	"full-screenshot": "Usage: everyapi full-screenshot [--out FILE] [--json]",
	"hover":           "Usage: everyapi hover --element SELECTOR [--json]",
	"drag":            "Usage: everyapi drag --from SELECTOR --to SELECTOR [--json]",
	"upload":          "Usage: everyapi upload --element SELECTOR --file FILE [--json]",
	"tab":             "Usage: everyapi tab <create|list|show|current|profile|switch|close> [flags]",
	"cookie":          "Usage: everyapi cookie <list|get|set|clear> [flags]",
	"storage":         "Usage: everyapi storage <local|session> <list|get|set|clear> [flags]",
	"console":         "Usage: everyapi console <list|clear> [--json]",
	"network":         "Usage: everyapi network <list|clear> [--json]",
	"clipboard":       "Usage: everyapi clipboard <read|write> [flags]",
	"dialog":          "Usage: everyapi dialog <get|accept|dismiss|close> [--json]",
	"download":        "Usage: everyapi download <list|save|clear> [flags]",
	"highlight":       "Usage: everyapi highlight --element SELECTOR [--json]",
	"capture":         "Usage: everyapi capture <start|stop|status> [--json]",
	"viewport":        "Usage: everyapi viewport --size WIDTHxHEIGHT [--json]",
	"geolocation":     "Usage: everyapi geolocation --value LAT,LON [--json]",
	"intercept":       "Usage: everyapi intercept <list|add|remove> [flags]",
	"mouse":           "Usage: everyapi mouse <event> [flags]",
	"inserttext":      "Usage: everyapi inserttext --text TEXT [--json]",
	"is":              "Usage: everyapi is --element SELECTOR [--json]",
	"scrollintoview":  "Usage: everyapi scrollintoview --element SELECTOR [--json]",
	"dblclick":        "Usage: everyapi dblclick --element SELECTOR [--json]",
	"forward":         "Usage: everyapi forward [--json]",
	"set":             "Usage: everyapi set <device|offline|headers|credentials|media|viewport|geolocation> [flags]",
	"exec":            "Usage: everyapi exec --command \"BROWSER_COMMAND [ARGS...]\" [--json]",
	"diagnostics":     "Usage: everyapi diagnostics memory [--json]",
	"agent-context":   "Usage: everyapi agent-context [--json]",
	"account":         "Usage: everyapi account <add|list> [flags]",
	"agent":           "Usage: everyapi agent <list|status|start|run|create|stop|kill|close> [flags]",
	"skills":          "Usage: everyapi skills <installed|share|list|get|install|update> [flags]",
	"orchestration":   "Usage: everyapi orchestration <run-create|run-use|run-current|run-list|run-show|send|check|ask|reply|inbox|task-create|task-list|task-update|dispatch|dispatch-show|worker-start|worker-show|worker-read|worker-stop|worker-abandon|worker-release|worker-retain|worker-list|coordinator-start|coordinator-stop|gate-create|gate-resolve|gate-list|reset> [flags]",
	"automations":     "Usage: everyapi automations <snapshot|list|show|create|edit|remove|run|runs> [flags]",
	"environment":     "Usage: everyapi environment <add|list|show|rm> [flags]",
	"project":         "Usage: everyapi project <list|setups|setup-existing-folder|setup-clone|setup-create|setup-update|setup-delete> [flags]",
	"file":            "Usage: everyapi file <open|diff|open-changed> [path] [flags]",
	"linear":          "Usage: everyapi linear <issue|search|list> [flags]",
	"vm":              "Usage: everyapi vm <recipe list|recipe doctor|runtime list|runtime show|runtime create|runtime suspend|runtime resume|runtime cleanup|runtime cancel|runtime cleanup-info|runtime forget> [flags]",
	"emulator":        "Usage: everyapi emulator <list|attach|tap|type|gesture|button|rotate|exec|kill> [flags]",
}

func commandHelp(name string) (any, error) {
	if usage, ok := nativeUsages[name]; ok {
		return usage, nil
	}
	return nil, unsupported(name)
}

func unsupported(name string) error {
	return fmt.Errorf("unknown native command %q", name)
}

func hasFlag(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func hasHelpWord(args []string) bool {
	for i, arg := range args {
		if arg != "help" {
			continue
		}
		// A literal value immediately following a long flag belongs to that
		// flag (for example --text help), not to the command dispatcher.
		if i > 0 && strings.HasPrefix(args[i-1], "--") && !strings.Contains(args[i-1], "=") {
			continue
		}
		return true
	}
	return false
}

func stripFlag(args []string, value string) []string {
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == value {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

// stripGlobalFlags removes routing metadata that is meaningful to the remote
// CLI but has no local side effect. Keeping the values out of positional
// parsing is important: `--environment env-1` must not become a file path or
// a terminal name in the local compatibility backend.
func stripGlobalFlags(args []string) []string {
	filtered := make([]string, 0, len(args))
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--json" || arg == "--help" || arg == "-h" {
			filtered = append(filtered, arg)
			continue
		}
		if arg == "--environment" || arg == "--pairing-code" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "--environment=") || strings.HasPrefix(arg, "--pairing-code=") {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

func stripRoutingFlags(command string, args []string) []string {
	stripWorktree := command == "exec" || command == "tab" || command == "set" || command == "cookie" || command == "storage" || command == "console" || command == "network" || command == "clipboard" || command == "dialog" || command == "download" || command == "highlight" || command == "capture" || command == "viewport" || command == "geolocation" || command == "intercept" || command == "mouse" || command == "inserttext" || command == "computer"
	if !stripWorktree {
		switch command {
		case "snapshot", "goto", "find", "get", "screenshot", "full-screenshot", "click", "fill", "type", "select", "scroll", "back", "reload", "eval", "wait", "check", "uncheck", "focus", "clear", "select-all", "keypress", "pdf", "hover", "drag", "upload", "scrollintoview", "dblclick", "forward", "is":
			stripWorktree = true
		}
	}
	if !stripWorktree {
		return args
	}
	filtered := make([]string, 0, len(args))
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--worktree" || arg == "--page" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "--worktree=") || strings.HasPrefix(arg, "--page=") {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

func preserveFlag(original, filtered []string, name string) []string {
	value := flagValue(original, name, "")
	if value == "" {
		return filtered
	}
	return append(filtered, "--"+name, value)
}

func flagValueAny(args []string, names ...string) string {
	for _, name := range names {
		if value := flagValue(args, name, ""); value != "" {
			return value
		}
	}
	return ""
}

func printData(v any) error {
	switch value := v.(type) {
	case string:
		_, err := fmt.Println(value)
		return err
	default:
		return json.NewEncoder(os.Stdout).Encode(value)
	}
}

func commandOutput(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), message)
	}
	return bytes.TrimSpace(out), nil
}

func gitRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	out, err := commandOutput(abs, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository: %w", abs, err)
	}
	return string(out), nil
}

func currentRoot() (string, error) { return gitRoot(".") }

func resolveRepoSelector(selector string) string {
	selector = strings.TrimSpace(selector)
	for _, prefix := range []string{"path:", "repo:", "id:"} {
		if strings.HasPrefix(selector, prefix) {
			selector = strings.TrimPrefix(selector, prefix)
			break
		}
	}
	if selector == "" {
		return "."
	}
	if info, err := inspectRepo(selector); err == nil {
		return info.Path
	}
	if repos, err := registeredRepos(); err == nil {
		for _, repo := range repos {
			if repo.Path == selector || repo.Name == selector || strings.TrimPrefix(repo.Path, "/") == selector {
				return repo.Path
			}
		}
	}
	return selector
}

func rootFromRepoFlag(args []string) (string, error) {
	selector := flagValue(args, "repo", "")
	if selector == "" {
		return currentRoot()
	}
	return gitRoot(resolveRepoSelector(selector))
}

type repoInfo struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Branch string `json:"branch,omitempty"`
	Remote string `json:"remote,omitempty"`
	Base   string `json:"baseRef,omitempty"`
}

func inspectRepo(path string) (repoInfo, error) {
	root, err := gitRoot(path)
	if err != nil {
		return repoInfo{}, err
	}
	branch, _ := commandOutput(root, "git", "branch", "--show-current")
	remote, _ := commandOutput(root, "git", "config", "--get", "remote.origin.url")
	return repoInfo{Path: root, Name: filepath.Base(root), Branch: string(branch), Remote: string(remote), Base: baseRef(root)}, nil
}

func baseRef(root string) string {
	if ref, err := commandOutput(root, "git", "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(string(ref), "refs/remotes/origin/")
	}
	if ref, err := commandOutput(root, "git", "config", "--get", "init.defaultBranch"); err == nil && len(ref) > 0 {
		return string(ref)
	}
	return "main"
}

func repo(args []string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return "Usage: everyapi repo <list|show|add|set-base-ref|search-refs> [flags]", nil
	}
	switch args[0] {
	case "list":
		return registeredRepos()
	case "show":
		path := flagValue(args[1:], "repo", firstPath(args[1:]))
		if path == "" {
			path = "."
		}
		return inspectRepo(path)
	case "add":
		path := flagValue(args[1:], "path", firstPath(args[1:]))
		if path == "" {
			path = "."
		}
		info, err := inspectRepo(path)
		if err != nil {
			return nil, err
		}
		repos, _ := registeredRepos()
		found := false
		for _, existing := range repos {
			if existing.Path == info.Path {
				found = true
			}
		}
		if !found {
			repos = append(repos, info)
			if err := saveRepos(repos); err != nil {
				return nil, err
			}
		}
		return info, nil
	case "set-base-ref":
		path := flagValue(args[1:], "repo", ".")
		if first := firstPath(args[1:]); first != "" && flagValue(args[1:], "repo", "") == "" {
			path = first
		}
		ref := flagValue(args[1:], "ref", "")
		if ref == "" {
			return nil, errors.New("--ref is required")
		}
		root, err := gitRoot(path)
		if err != nil {
			return nil, err
		}
		if _, err := commandOutput(root, "git", "config", "everyapi.baseRef", ref); err != nil {
			return nil, err
		}
		return inspectRepo(root)
	case "search-refs":
		path := flagValue(args[1:], "repo", ".")
		query := flagValue(args[1:], "query", firstPath(args[1:]))
		limit := intValue(args[1:], "limit", 50)
		root, err := gitRoot(path)
		if err != nil {
			return nil, err
		}
		refs, err := commandOutput(root, "git", "for-each-ref", "--format=%(refname:short)")
		if err != nil {
			return nil, err
		}
		var found []string
		for _, ref := range strings.Split(string(refs), "\n") {
			if ref != "" && (query == "" || strings.Contains(strings.ToLower(ref), strings.ToLower(query))) {
				found = append(found, ref)
				if len(found) >= limit {
					break
				}
			}
		}
		return found, nil
	default:
		return nil, fmt.Errorf("unknown repo subcommand %q", args[0])
	}
}

func registeredRepos() ([]repoInfo, error) {
	path, err := statePath("repositories.json")
	if err != nil {
		return nil, err
	}
	var repos []repoInfo
	if data, e := os.ReadFile(path); e == nil {
		if e := json.Unmarshal(data, &repos); e != nil {
			return nil, e
		}
	}
	if current, e := inspectRepo("."); e == nil {
		found := false
		for _, repo := range repos {
			if samePath(repo.Path, current.Path) {
				found = true
			}
		}
		if !found {
			repos = append(repos, current)
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Path < repos[j].Path })
	return repos, nil
}

func saveRepos(repos []repoInfo) error {
	path, err := statePath("repositories.json")
	if err != nil {
		return err
	}
	return os.WriteFile(path, mustJSON(repos), 0o600)
}

type worktreeInfo struct {
	Path            string `json:"path"`
	HEAD            string `json:"head"`
	Branch          string `json:"branch,omitempty"`
	Dirty           bool   `json:"dirty"`
	Terminal        string `json:"terminal,omitempty"`
	DisplayName     string `json:"displayName,omitempty"`
	Issue           string `json:"issue,omitempty"`
	LinearIssue     string `json:"linearIssue,omitempty"`
	Comment         string `json:"comment,omitempty"`
	WorkspaceStatus string `json:"workspaceStatus,omitempty"`
	ParentWorktree  string `json:"parentWorktree,omitempty"`
}

func parseWorktrees(root string) ([]worktreeInfo, error) {
	out, err := commandOutput(root, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []worktreeInfo
	var current *worktreeInfo
	flush := func() {
		if current != nil {
			list = append(list, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &worktreeInfo{Path: strings.TrimPrefix(line, "worktree ")}
		case current != nil && strings.HasPrefix(line, "HEAD "):
			current.HEAD = strings.TrimPrefix(line, "HEAD ")
		case current != nil && strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	for i := range list {
		if status, e := commandOutput(list[i].Path, "git", "status", "--porcelain"); e == nil {
			list[i].Dirty = len(status) > 0
		}
	}
	if metadata, metadataErr := loadState("worktree-metadata.json"); metadataErr == nil {
		for i := range list {
			for _, raw := range metadata.Items {
				if metadataString(raw["path"]) != list[i].Path {
					continue
				}
				list[i].DisplayName = metadataString(raw["display-name"])
				list[i].Issue = metadataString(raw["issue"])
				list[i].LinearIssue = metadataString(raw["linear-issue"])
				list[i].Comment = metadataString(raw["comment"])
				list[i].WorkspaceStatus = metadataString(raw["workspace-status"])
				list[i].ParentWorktree = metadataString(raw["parent-worktree"])
			}
		}
	}
	return list, nil
}

func worktree(args []string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return "Usage: everyapi worktree <list|current|show|create|set|rm|ps> [flags]", nil
	}
	root, err := rootFromRepoFlag(args[1:])
	if err != nil {
		return nil, err
	}
	switch args[0] {
	case "list", "ps":
		list, e := parseWorktrees(root)
		limit := intValue(args[1:], "limit", 0)
		if limit == 0 && args[0] == "ps" {
			limit = 10
		}
		if limit > 0 && len(list) > limit {
			list = list[:limit]
		}
		return list, e
	case "current":
		cwd, _ := os.Getwd()
		list, e := parseWorktrees(root)
		if e != nil {
			return nil, e
		}
		for _, wt := range list {
			if samePath(cwd, wt.Path) || isWithin(cwd, wt.Path) {
				return wt, nil
			}
		}
		return nil, errors.New("current directory is not a git worktree")
	case "show":
		selector := flagValue(args[1:], "worktree", firstPath(args[1:]))
		return selectWorktree(root, selector)
	case "create":
		name := flagValue(args[1:], "name", "")
		if name == "" {
			return nil, errors.New("--name is required")
		}
		path := flagValue(args[1:], "path", filepath.Join(filepath.Dir(root), name))
		branch := flagValue(args[1:], "base-branch", baseRef(root))
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("worktree path already exists: %s", path)
		}
		if _, err := commandOutput(root, "git", "worktree", "add", "-b", name, path, branch); err != nil {
			return nil, err
		}
		created, err := selectWorktree(root, path)
		if err != nil {
			return nil, err
		}
		if agent := flagValue(args[1:], "agent", ""); agent != "" && runtime.GOOS != "windows" {
			session := "everyapi-" + strings.NewReplacer("/", "-", " ", "-").Replace(name)
			if _, e := commandOutput(root, "tmux", "new-session", "-d", "-s", session, agent); e == nil {
				created.Terminal = session
				if prompt := flagValue(args[1:], "prompt", ""); prompt != "" {
					_, _ = commandOutput(root, "tmux", "send-keys", "-t", session, prompt, "Enter")
				}
			}
		}
		metadata, metadataErr := loadState("worktree-metadata.json")
		if metadataErr != nil {
			return nil, metadataErr
		}
		item := map[string]any{"path": created.Path, "branch": created.Branch}
		for _, field := range []string{"display-name", "issue", "linear-issue", "comment", "workspace-status", "parent-worktree", "setup", "project", "host", "project-host-setup"} {
			if value := flagValue(args[1:], field, ""); value != "" {
				item[field] = value
			}
		}
		if hasFlag(args[1:], "--no-parent") {
			delete(item, "parent-worktree")
		}
		metadata.Items = upsertStateItem(metadata.Items, created.Path, item)
		if saveErr := saveState("worktree-metadata.json", metadata); saveErr != nil {
			return nil, saveErr
		}
		created.DisplayName = metadataString(item["display-name"])
		created.Issue = metadataString(item["issue"])
		created.LinearIssue = metadataString(item["linear-issue"])
		created.Comment = metadataString(item["comment"])
		created.WorkspaceStatus = metadataString(item["workspace-status"])
		created.ParentWorktree = metadataString(item["parent-worktree"])
		return created, nil
	case "set":
		selector := flagValue(args[1:], "worktree", firstPath(args[1:]))
		if selector == "" {
			return nil, errors.New("--worktree is required")
		}
		wt, e := selectWorktree(root, selector)
		if e != nil {
			return nil, e
		}
		metadata, e := loadState("worktree-metadata.json")
		if e != nil {
			return nil, e
		}
		key := wt.Path
		item := map[string]any{"path": wt.Path, "branch": wt.Branch}
		for _, field := range []string{"display-name", "issue", "linear-issue", "comment", "workspace-status", "parent-worktree"} {
			if value := flagValue(args[1:], field, ""); value != "" {
				item[field] = value
			}
		}
		if hasFlag(args[1:], "--no-parent") {
			delete(item, "parent-worktree")
		}
		metadata.Items = upsertStateItem(metadata.Items, key, item)
		if e := saveState("worktree-metadata.json", metadata); e != nil {
			return nil, e
		}
		return item, nil
	case "rm", "remove", "delete":
		selector := flagValue(args[1:], "worktree", firstPath(args[1:]))
		wt, e := selectWorktree(root, selector)
		if e != nil {
			return nil, e
		}
		removeArgs := []string{"worktree", "remove"}
		if hasFlag(args[1:], "--force") {
			removeArgs = append(removeArgs, "--force")
		}
		removeArgs = append(removeArgs, wt.Path)
		if _, e = commandOutput(root, "git", removeArgs...); e != nil {
			return nil, e
		}
		if metadata, metadataErr := loadState("worktree-metadata.json"); metadataErr == nil {
			kept := metadata.Items[:0]
			for _, item := range metadata.Items {
				if metadataString(item["path"]) != wt.Path {
					kept = append(kept, item)
				}
			}
			metadata.Items = kept
			_ = saveState("worktree-metadata.json", metadata)
		}
		return map[string]any{"removed": wt.Path, "hooks": hasFlag(args[1:], "--run-hooks")}, nil
	default:
		return nil, fmt.Errorf("unknown worktree subcommand %q", args[0])
	}
}

func selectWorktree(root, selector string) (worktreeInfo, error) {
	list, err := parseWorktrees(root)
	if err != nil {
		return worktreeInfo{}, err
	}
	rawSelector := strings.TrimSpace(selector)
	selector = rawSelector
	for _, prefix := range []string{"path:", "branch:", "name:", "issue:", "linear-issue:"} {
		if strings.HasPrefix(selector, prefix) {
			value := strings.TrimPrefix(selector, prefix)
			if prefix == "path:" || prefix == "branch:" {
				selector = value
				break
			}
			metadata, metadataErr := loadState("worktree-metadata.json")
			if metadataErr == nil {
				for _, item := range metadata.Items {
					key := "display-name"
					if prefix == "issue:" {
						key = "issue"
					}
					if prefix == "linear-issue:" {
						key = "linear-issue"
					}
					if metadataString(item[key]) == value {
						selector = fmt.Sprint(item["path"])
						break
					}
				}
			}
			break
		}
	}
	if strings.HasPrefix(selector, "id:") {
		selector = strings.TrimPrefix(selector, "id:")
		if separator := strings.Index(selector, "::"); separator >= 0 {
			selector = selector[separator+2:]
		}
	}
	if selector == "" || selector == "active" || selector == "current" {
		cwd, _ := os.Getwd()
		for _, wt := range list {
			if samePath(cwd, wt.Path) || isWithin(cwd, wt.Path) {
				return wt, nil
			}
		}
	}
	for _, wt := range list {
		if samePath(selector, wt.Path) || selector == wt.Branch || strings.HasSuffix(wt.Path, selector) {
			return wt, nil
		}
	}
	return worktreeInfo{}, fmt.Errorf("worktree %q not found", rawSelector)
}

func samePath(a, b string) bool {
	aa := canonicalPath(a)
	bb := canonicalPath(b)
	return aa == bb
}

func metadataString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func canonicalPath(value string) string {
	abs, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

func isWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type terminalInfo struct {
	Name    string `json:"name"`
	Created string `json:"created,omitempty"`
}

func terminal(args []string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return "Usage: everyapi terminal <list|show|read|send|wait|stop|create|rename|split|switch|focus|close> [flags]", nil
	}
	if runtime.GOOS == "windows" {
		return localTerminal(args)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return localTerminal(args)
	}
	switch args[0] {
	case "list":
		out, err := commandOutput(".", "tmux", "list-sessions", "-F", "#{session_name}\t#{session_created}")
		if err != nil {
			if strings.Contains(err.Error(), "no server running") {
				return []terminalInfo{}, nil
			}
			return nil, err
		}
		var list []terminalInfo
		for _, line := range strings.Split(string(out), "\n") {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) > 0 && parts[0] != "" {
				item := terminalInfo{Name: parts[0]}
				if len(parts) == 2 {
					item.Created = parts[1]
				}
				list = append(list, item)
			}
		}
		if limit := intValue(args[1:], "limit", 0); limit > 0 && len(list) > limit {
			list = list[:limit]
		}
		return list, nil
	case "read":
		name := terminalName(args[1:])
		lines := intValue(args[1:], "limit", 200)
		if hasFlag(args[1:], "--screen") {
			lines = intValue(args[1:], "screen", lines)
		}
		out, err := commandOutput(".", "tmux", "capture-pane", "-p", "-S", "-"+strconv.Itoa(lines), "-t", name)
		return string(out), err
	case "show":
		name := terminalName(args[1:])
		out, err := commandOutput(".", "tmux", "capture-pane", "-p", "-S", "-80", "-t", name)
		return map[string]any{"terminal": name, "preview": string(out)}, err
	case "send":
		name := terminalName(args[1:])
		text := terminalText(args[1:])
		if text == "" && !hasFlag(args[1:], "--interrupt") {
			return nil, errors.New("--text is required")
		}
		keys := []string{"send-keys", "-t", name}
		if hasFlag(args[1:], "--interrupt") {
			keys = append(keys, "C-c")
		} else {
			keys = append(keys, text)
			// Preserve the historical behavior (send a line) while accepting
			// the reference CLI's explicit --enter switch.
			keys = append(keys, "Enter")
		}
		_, err := commandOutput(".", "tmux", keys...)
		return map[string]any{"sent": true, "terminal": name}, err
	case "wait":
		name := terminalName(args[1:])
		timeout, timeoutErr := timeoutDuration(args[1:], 30*time.Second)
		if timeoutErr != nil {
			return nil, timeoutErr
		}
		deadline := time.Now().Add(timeout)
		for {
			if _, err := commandOutput(".", "tmux", "has-session", "-t", name); err != nil {
				return map[string]any{"terminal": name, "exited": true}, nil
			}
			if !time.Now().Before(deadline) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		return map[string]any{"terminal": name, "exited": false, "timedOut": true}, nil
	case "create":
		name := terminalName(args[1:])
		command := flagValue(args[1:], "command", os.Getenv("SHELL"))
		if command == "" {
			command = "sh"
		}
		newArgs := []string{"new-session", "-d", "-s", name}
		if title := flagValue(args[1:], "title", ""); title != "" {
			newArgs = append(newArgs, "-n", title)
		}
		newArgs = append(newArgs, command)
		_, err := commandOutput(".", "tmux", newArgs...)
		result := map[string]any{"terminal": name, "command": command, "title": flagValue(args[1:], "title", "")}
		if hasFlag(args[1:], "--focus") && err == nil {
			_, _ = commandOutput(".", "tmux", "select-window", "-t", name)
			result["focused"] = true
		}
		return result, err
	case "stop", "close", "kill":
		name := terminalName(args[1:])
		_, err := commandOutput(".", "tmux", "kill-session", "-t", name)
		return map[string]any{"terminal": name, "closed": true, "stopped": args[0] == "stop"}, err
	case "rename":
		name := terminalName(args[1:])
		title := flagValue(args[1:], "title", flagValue(args[1:], "name", ""))
		if title == "" {
			return nil, errors.New("--title is required")
		}
		_, err := commandOutput(".", "tmux", "rename-window", "-t", name, title)
		return map[string]any{"terminal": name, "title": title}, err
	case "split":
		name := terminalName(args[1:])
		splitArgs := []string{"split-window", "-t", name}
		if direction := strings.ToLower(flagValue(args[1:], "direction", "")); direction != "" {
			switch direction {
			case "vertical":
				splitArgs = append(splitArgs, "-v")
			case "horizontal":
				splitArgs = append(splitArgs, "-h")
			}
		}
		if command := flagValue(args[1:], "command", ""); command != "" {
			splitArgs = append(splitArgs, command)
		}
		_, err := commandOutput(".", "tmux", splitArgs...)
		return map[string]any{"terminal": name, "split": true, "direction": flagValue(args[1:], "direction", "")}, err
	case "switch", "focus":
		name := terminalName(args[1:])
		_, err := commandOutput(".", "tmux", "select-window", "-t", name)
		return map[string]any{"terminal": name, "focused": true}, err
	default:
		return nil, fmt.Errorf("unknown terminal subcommand %q", args[0])
	}
}

func localTerminal(args []string) (any, error) {
	store, err := loadState("terminals.json")
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return store.Items, nil
	}
	name := terminalName(args[1:])
	find := func(id string) (map[string]any, int) {
		for i, item := range store.Items {
			if item["id"] == id || item["name"] == id || item["terminal"] == id {
				return item, i
			}
		}
		return nil, -1
	}
	switch args[0] {
	case "list":
		items := store.Items
		if limit := intValue(args[1:], "limit", 0); limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		return items, nil
	case "create":
		item := map[string]any{"id": name, "name": name, "command": flagValue(args[1:], "command", "sh"), "status": "running", "output": "", "createdAt": time.Now().UTC().Format(time.RFC3339)}
		if title := flagValue(args[1:], "title", ""); title != "" {
			item["title"] = title
		}
		store.Items = upsertStateItem(store.Items, name, item)
		if hasFlag(args[1:], "--focus") {
			store.Current = name
		}
		return item, saveState("terminals.json", store)
	case "show", "read":
		item, _ := find(name)
		if item == nil {
			return nil, fmt.Errorf("terminal %q not found", name)
		}
		if args[0] == "read" {
			output := fmt.Sprint(item["output"])
			if limit := intValue(args[1:], "limit", 0); limit > 0 {
				lines := strings.Split(output, "\n")
				if len(lines) > limit {
					lines = lines[len(lines)-limit:]
				}
				output = strings.Join(lines, "\n")
			}
			return output, nil
		}
		return item, nil
	case "send":
		item, _ := find(name)
		if item == nil {
			return nil, fmt.Errorf("terminal %q not found", name)
		}
		text := terminalText(args[1:])
		if text == "" && !hasFlag(args[1:], "--interrupt") {
			return nil, errors.New("--text is required")
		}
		if hasFlag(args[1:], "--interrupt") {
			item["output"] = fmt.Sprintf("%s^C\n", item["output"])
		} else {
			item["output"] = fmt.Sprintf("%s%s\n", item["output"], text)
		}
		return map[string]any{"terminal": name, "sent": true, "entered": hasFlag(args[1:], "--enter")}, saveState("terminals.json", store)
	case "wait":
		item, _ := find(name)
		if item == nil {
			return map[string]any{"terminal": name, "exited": true}, nil
		}
		timeout, timeoutErr := timeoutDuration(args[1:], 30*time.Second)
		if timeoutErr != nil {
			return nil, timeoutErr
		}
		exited := item["status"] == "stopped" || item["status"] == "exited"
		result := map[string]any{"terminal": name, "exited": exited, "status": item["status"], "for": flagValue(args[1:], "for", "exit")}
		if !exited && timeout == 0 {
			result["timedOut"] = true
		}
		return result, nil
	case "stop", "close", "kill":
		item, index := find(name)
		if item == nil {
			return nil, fmt.Errorf("terminal %q not found", name)
		}
		if args[0] == "stop" {
			item["status"] = "stopped"
		} else {
			store.Items = append(store.Items[:index], store.Items[index+1:]...)
		}
		return map[string]any{"terminal": name, "closed": args[0] != "stop", "stopped": args[0] == "stop"}, saveState("terminals.json", store)
	case "rename":
		item, _ := find(name)
		if item == nil {
			return nil, fmt.Errorf("terminal %q not found", name)
		}
		title := flagValue(args[1:], "title", flagValue(args[1:], "name", ""))
		if title == "" {
			return nil, errors.New("--title is required")
		}
		item["title"] = title
		return item, saveState("terminals.json", store)
	case "split":
		item := map[string]any{"id": name + "-split-" + strconv.FormatInt(time.Now().UnixNano(), 10), "name": name + "-split", "parent": name, "status": "running", "output": ""}
		item["direction"] = flagValue(args[1:], "direction", "")
		item["command"] = flagValue(args[1:], "command", "")
		store.Items = append(store.Items, item)
		return item, saveState("terminals.json", store)
	case "switch", "focus":
		if item, _ := find(name); item != nil {
			store.Current = name
			return map[string]any{"terminal": name, "focused": true}, saveState("terminals.json", store)
		}
		return nil, fmt.Errorf("terminal %q not found", name)
	default:
		return nil, fmt.Errorf("unknown terminal subcommand %q", args[0])
	}
}

func terminalName(args []string) string {
	name := flagValue(args, "terminal", "")
	if name == "" {
		name = flagValue(args, "name", firstPath(args))
	}
	if name == "" {
		name = "everyapi"
	}
	return name
}

func terminalText(args []string) string {
	if text := flagValue(args, "text", ""); text != "" {
		return text
	}
	parts := positional(args)
	if flagValue(args, "terminal", "") == "" && flagValue(args, "name", "") == "" && len(parts) > 0 {
		parts = parts[1:]
	}
	return strings.Join(parts, " ")
}

func openRuntime(args []string) (any, error) {
	if location := flagValue(args, "url", ""); location != "" {
		if _, err := url.ParseRequestURI(location); err != nil {
			return nil, fmt.Errorf("invalid URL %q", location)
		}
		return map[string]any{"url": location, "opened": openLocation(location)}, nil
	}
	port := intValue(args, "port", 6768)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	ready := probeEndpoint(endpoint)
	var started any
	if !ready {
		started, _ = serve([]string{"--background", "--port", strconv.Itoa(port)})
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if probeEndpoint(endpoint) {
				ready = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	result := map[string]any{"ready": ready, "endpoint": endpoint, "local": true, "started": started}
	if !ready {
		return result, fmt.Errorf("runtime endpoint %s did not become reachable", endpoint)
	}
	return result, nil
}

func probeEndpoint(endpoint string) bool {
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get(endpoint)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < http.StatusInternalServerError
}

func openLocation(location string) bool {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", location).Start() == nil
	case "windows":
		return exec.Command("cmd", "/c", "start", "", location).Start() == nil
	default:
		return exec.Command("xdg-open", location).Start() == nil
	}
}

func serve(args []string) (any, error) {
	root := flagValue(args, "project-root", ".")
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if info, e := os.Stat(root); e != nil || !info.IsDir() {
		return nil, fmt.Errorf("project root is not a directory: %s", root)
	}
	port := intValue(args, "port", 6768)
	if hasFlag(args, "--json") || hasFlag(args, "--background") {
		address := fmt.Sprintf("127.0.0.1:%d", port)
		probeListener, listenErr := net.Listen("tcp", address)
		if listenErr != nil {
			return nil, fmt.Errorf("runtime port %d is unavailable: %w", port, listenErr)
		}
		_ = probeListener.Close()
		command := exec.Command(os.Args[0], "serve", "--project-root", root, "--port", strconv.Itoa(port))
		logPath, logErr := statePath("serve.log")
		if logErr != nil {
			return nil, logErr
		}
		logFile, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if logErr != nil {
			return nil, logErr
		}
		command.Stdout = logFile
		command.Stderr = logFile
		detachCommand(command)
		if err := command.Start(); err != nil {
			_ = logFile.Close()
			return nil, fmt.Errorf("start background server: %w", err)
		}
		_ = logFile.Close()
		endpoint := "http://" + address
		result := map[string]any{"endpoint": endpoint, "projectRoot": root, "foreground": false, "pid": command.Process.Pid, "log": logPath}
		deadline := time.Now().Add(2 * time.Second)
		for !probeEndpoint(endpoint) && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if !probeEndpoint(endpoint) {
			_ = command.Process.Kill()
			return result, fmt.Errorf("runtime endpoint %s did not become reachable", endpoint)
		}
		return result, nil
	}
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: http.FileServer(http.Dir(root))}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, err
	}
	endpoint := "http://" + listener.Addr().String()
	go func() { _ = server.Serve(listener) }()
	fmt.Printf("serving %s at %s (press Ctrl-C to stop)\n", root, endpoint)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	_ = server.Shutdown(context.Background())
	return map[string]any{"endpoint": endpoint, "projectRoot": root, "foreground": true}, nil
}

func status(args []string) (any, error) {
	port := intValue(args, "port", 6768)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	root, err := currentRoot()
	if err != nil {
		return map[string]any{"ready": probeEndpoint(endpoint), "app": map[string]any{"ready": true}, "runtime": map[string]any{"ready": probeEndpoint(endpoint), "endpoint": endpoint}, "graph": map[string]any{"ready": false}, "git": false, "cwd": mustGetwd()}, nil
	}
	info, err := inspectRepo(root)
	if err != nil {
		return nil, err
	}
	worktrees, _ := parseWorktrees(root)
	runtimeReady := probeEndpoint(endpoint)
	return map[string]any{"ready": runtimeReady, "app": map[string]any{"ready": true, "cwd": mustGetwd()}, "runtime": map[string]any{"ready": runtimeReady, "endpoint": endpoint}, "graph": map[string]any{"ready": len(worktrees) > 0, "worktreeCount": len(worktrees)}, "cwd": mustGetwd(), "repo": info, "worktreeCount": len(worktrees)}, nil
}

func host(args []string) (any, error) {
	hostname, _ := os.Hostname()
	info := map[string]any{"hostname": hostname, "os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(), "cwd": mustGetwd()}
	if len(args) == 0 || args[0] == "show" {
		return info, nil
	}
	if args[0] == "list" {
		return []map[string]any{info}, nil
	}
	return nil, fmt.Errorf("unknown host subcommand %q", args[0])
}

func claudeTeams(args []string) (any, error) {
	command := flagValue(args, "command", "claude")
	if command == "" {
		return nil, errors.New("--command cannot be empty")
	}
	cmd := exec.Command(command, positional(args)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("agent command failed: %w", err)
	}
	return map[string]any{"command": command, "started": true}, nil
}

func localExec(args []string) (any, error) {
	command := flagValue(args, "command", "")
	operands := positional(args)
	if command == "" && len(operands) > 0 {
		command, operands = operands[0], operands[1:]
	}
	if command == "" {
		return nil, errors.New("a command is required (use --command or a positional command)")
	}
	cmd := exec.Command(command, operands...)
	cmd.Dir = mustGetwd()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	response := map[string]any{"command": command, "args": operands, "stdout": stdout.String(), "stderr": stderr.String()}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			response["exitCode"] = exitErr.ExitCode()
		}
		return response, fmt.Errorf("local command failed: %w", err)
	}
	response["exitCode"] = 0
	return response, nil
}

func browserExec(args []string) (any, error) {
	if hasFlag(args, "--local") {
		return localExec(args)
	}
	command := flagValue(args, "command", strings.Join(positional(args), " "))
	parts, parseErr := splitCommand(command)
	if parseErr != nil {
		return nil, parseErr
	}
	if len(parts) == 0 {
		return nil, errors.New("a browser command is required (use --command)")
	}
	name := parts[0]
	if name == "exec" {
		return nil, errors.New("nested exec is not allowed")
	}
	innerArgs := parts[1:]
	if hasFlag(innerArgs, "--json") {
		filtered := innerArgs[:0]
		for _, arg := range innerArgs {
			if arg != "--json" {
				filtered = append(filtered, arg)
			}
		}
		innerArgs = filtered
	}
	var data any
	var err error
	switch name {
	case "snapshot", "goto", "find", "get", "screenshot", "full-screenshot", "pdf", "click", "fill", "type", "select", "scroll", "back", "reload", "eval", "wait", "check", "uncheck", "focus", "clear", "select-all", "keypress", "hover", "drag", "upload", "scrollintoview", "dblclick", "forward", "is":
		data, err = browser(innerArgs, name)
	case "tab":
		data, err = tabs(innerArgs)
	case "cookie", "storage", "console", "network", "clipboard", "dialog", "download", "highlight", "capture", "viewport", "geolocation", "intercept", "mouse", "inserttext", "set":
		data, err = browserAux(innerArgs, name)
	default:
		return nil, fmt.Errorf("unsupported browser command %q", name)
	}
	return map[string]any{"command": command, "result": data}, err
}

func splitCommand(command string) ([]string, error) {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			parts = append(parts, current.String())
			current.Reset()
		}
	}
	for _, r := range command {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		current.WriteByte('\\')
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote in browser command")
	}
	flush()
	return parts, nil
}

type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"-"`
}

func skills(args []string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return "Usage: everyapi skills <installed|share|list|get|install|update> [flags]", nil
	}
	list := discoverSkills()
	switch args[0] {
	case "list", "installed":
		return list, nil
	case "get", "show":
		topic := flagValue(args[1:], "topic", "")
		if topic == "" && len(positional(args[1:])) > 0 {
			topic = positional(args[1:])[0]
		}
		for _, skill := range list {
			if skill.Name == topic || filepath.Base(skill.Path) == topic {
				content, err := os.ReadFile(filepath.Join(skill.Path, "SKILL.md"))
				result := map[string]any{"skill": skill, "content": string(content)}
				if hasFlag(args[1:], "--full") {
					result["full"] = true
				}
				return result, err
			}
		}
		return nil, fmt.Errorf("skill %q not found", topic)
	case "share":
		selected := flagValues(args[1:], "skill")
		if len(selected) == 0 {
			selected = positional(args[1:])
		}
		if hasFlag(args[1:], "--all") || len(selected) == 0 {
			selected = make([]string, 0, len(list))
			for _, skill := range list {
				selected = append(selected, skill.Name)
			}
		}
		shared := make([]string, 0, len(selected))
		for _, selector := range selected {
			for _, skill := range list {
				if skill.Name == selector || filepath.Base(skill.Path) == selector {
					shared = append(shared, skill.Name)
					break
				}
			}
		}
		if len(shared) != len(selected) {
			return nil, errors.New("one or more selected skills were not found")
		}
		id := strconv.FormatInt(time.Now().UnixNano(), 36)
		sharePath, pathErr := statePath("skill-share-" + id + ".html")
		if pathErr != nil {
			return nil, pathErr
		}
		var page strings.Builder
		page.WriteString("<!doctype html><meta charset=\"utf-8\"><title>Shared skills</title><h1>Shared skills</h1>")
		for _, skillName := range shared {
			for _, skill := range list {
				if skill.Name != skillName {
					continue
				}
				content, readErr := os.ReadFile(filepath.Join(skill.Path, "SKILL.md"))
				if readErr != nil {
					return nil, readErr
				}
				page.WriteString("<article><h2>")
				page.WriteString(stdhtml.EscapeString(skill.Name))
				page.WriteString("</h2><pre>")
				page.WriteString(stdhtml.EscapeString(string(content)))
				page.WriteString("</pre></article>")
				break
			}
		}
		if err := os.WriteFile(sharePath, []byte(page.String()), 0o600); err != nil {
			return nil, err
		}
		share := map[string]any{"id": id, "url": "file://" + sharePath, "path": sharePath, "skills": shared, "createdAt": time.Now().UTC().Format(time.RFC3339)}
		if bundle := flagValue(args[1:], "bundle-name", ""); bundle != "" {
			share["bundleName"] = bundle
		}
		if notes := flagValue(args[1:], "release-notes", ""); notes != "" {
			share["releaseNotes"] = notes
		}
		shares, err := loadState("skill-shares.json")
		if err != nil {
			return nil, err
		}
		shares.Items = append(shares.Items, share)
		return share, saveState("skill-shares.json", shares)
	case "install", "update":
		selected := flagValues(args[1:], "skill")
		if len(selected) == 0 {
			selected = positional(args[1:])
		}
		if hasFlag(args[1:], "--all") {
			selected = selected[:0]
			for _, skill := range list {
				selected = append(selected, skill.Name)
			}
		}
		if len(selected) == 0 {
			return list, nil
		}
		destination := filepath.Join(".agents", "skills")
		if err := os.MkdirAll(destination, 0o700); err != nil {
			return nil, err
		}
		var installed []string
		for _, selector := range selected {
			var source string
			for _, skill := range list {
				if skill.Name == selector || filepath.Base(skill.Path) == selector {
					source = skill.Path
					break
				}
			}
			if source == "" {
				return nil, fmt.Errorf("skill %q not found", selector)
			}
			target := filepath.Join(destination, filepath.Base(source))
			if hasFlag(args[1:], "--dry-run") {
				installed = append(installed, target)
				continue
			}
			if err := copyDirectory(source, target); err != nil {
				return nil, err
			}
			installed = append(installed, target)
		}
		result := map[string]any{"updated": args[0] == "update", "skills": installed}
		if agent := flagValue(args[1:], "agent", ""); agent != "" {
			result["agent"] = agent
		}
		if hasFlag(args[1:], "--local") {
			result["local"] = true
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unknown skills subcommand %q", args[0])
	}
}

func discoverSkills() []skillInfo {
	roots := []string{"."}
	if dir := os.Getenv("EVERYAPI_SKILLS_DIR"); dir != "" {
		roots = append(roots, dir)
	}
	seen := map[string]bool{}
	var list []skillInfo
	for _, root := range roots {
		root, _ = filepath.Abs(root)
		stop := root
		if root == canonicalPath(".") {
			if repo, err := currentRoot(); err == nil {
				stop = repo
			}
		}
		for current := root; ; current = filepath.Dir(current) {
			dir := filepath.Join(current, ".agents", "skills")
			entries, err := os.ReadDir(dir)
			if err == nil {
				for _, entry := range entries {
					if !entry.IsDir() {
						continue
					}
					path := filepath.Join(dir, entry.Name())
					// A directory can be a namespace that contains several
					// skills (for example .agents/skills/gitnexus/*), not a
					// skill itself. Only expose installable skill directories;
					// otherwise share --all later tries to read a missing
					// SKILL.md and fails after listing a phantom entry.
					if info, statErr := os.Stat(filepath.Join(path, "SKILL.md")); statErr != nil || info.IsDir() {
						continue
					}
					if seen[path] {
						continue
					}
					seen[path] = true
					name, description := skillMetadata(path, entry.Name())
					// Keep the public command surface product-neutral. A skill may
					// carry a legacy provider name in either its directory or
					// front-matter; those entries are not exposed by this CLI.
					list = append(list, skillInfo{Name: name, Description: description, Path: path})
				}
			}
			if current == stop || current == filepath.Dir(current) {
				break
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

func skillMetadata(path, fallback string) (string, string) {
	data, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
	if err != nil {
		return fallback, ""
	}
	name, description := fallback, ""
	nameExplicit := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			nameExplicit = name != ""
		}
		if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
		if strings.HasPrefix(line, "# ") && !nameExplicit {
			name = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return name, description
}

func copyDirectory(source, target string) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		destination := filepath.Join(target, rel)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode().Perm()&0o600|0o600)
	})
}

func files(args []string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return "Usage: everyapi file <open|diff|open-changed> [path] [flags]", nil
	}
	root, err := currentRoot()
	if err != nil {
		return nil, err
	}
	switch args[0] {
	case "open":
		path := firstPath(args[1:])
		if path == "" {
			return nil, errors.New("a file path is required")
		}
		path = safeRepoPath(root, path)
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
		return map[string]any{"path": path, "opened": launchEditor(path)}, nil
	case "diff":
		path := firstPath(args[1:])
		if path == "" {
			return nil, errors.New("a file path is required")
		}
		gitArgs := []string{"diff"}
		if hasFlag(args[1:], "--staged") {
			gitArgs = append(gitArgs, "--cached")
		}
		gitArgs = append(gitArgs, "--", path)
		out, err := commandOutput(root, "git", gitArgs...)
		return map[string]any{"path": path, "diff": string(out)}, err
	case "open-changed":
		out, err := commandOutput(root, "git", "status", "--porcelain=v1")
		if err != nil {
			return nil, err
		}
		var paths []string
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) > 2 {
				// commandOutput trims leading whitespace from the complete git
				// response, so the first porcelain status may be two bytes
				// shorter than subsequent lines. Strip the two-byte status code
				// and surrounding separator instead of assuming a third byte.
				if path := strings.TrimSpace(line[2:]); path != "" {
					paths = append(paths, path)
				}
			}
		}
		return map[string]any{"files": paths, "count": len(paths)}, nil
	default:
		return nil, fmt.Errorf("unknown file subcommand %q", args[0])
	}
}

func launchEditor(path string) bool {
	if editor := os.Getenv("EDITOR"); editor != "" {
		return exec.Command(editor, path).Start() == nil
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start() == nil
	}
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "start", "", path).Start() == nil
	}
	return exec.Command("xdg-open", path).Start() == nil
}

func diagnostics(args []string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return map[string]any{"commands": []string{"memory"}}, nil
	}
	if args[0] != "memory" {
		return nil, fmt.Errorf("unknown diagnostics subcommand %q", args[0])
	}
	if runtime.GOOS == "windows" {
		out, err := commandOutput(".", "tasklist", "/FO", "CSV", "/NH")
		return string(out), err
	}
	out, err := commandOutput(".", "ps", "-axo", "pid,ppid,%cpu,%mem,comm")
	return string(out), err
}

func agentContext(args []string) (any, error) {
	commands := buildAgentSchema()
	return map[string]any{
		"schemaVersion": 1,
		"commandCount":  len(commands),
		"commands":      commands,
	}, nil
}

func buildAgentSchema() []map[string]any {
	commands := make([]map[string]any, 0, len(nativeCommandNames))
	for _, name := range nativeCommandNames {
		if name == "console" || name == "download" || name == "network" {
			commands = append(commands, schemaCommand(name))
			continue
		}
		if subs, ok := nativeSubcommands[name]; ok {
			for _, sub := range subs {
				command := name + " " + sub
				if !schemaCommandExposed(command) {
					continue
				}
				commands = append(commands, schemaCommand(command))
			}
			continue
		}
		commands = append(commands, schemaCommand(name))
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i]["command"].(string) < commands[j]["command"].(string) })
	return commands
}

func schemaCommandExposed(command string) bool {
	for _, hidden := range []string{
		"capture list", "capture status",
		"cookie clear", "cookie list",
		"dialog get", "dialog list",
		"download clear", "download list", "download save",
		"intercept add", "intercept remove",
		"network clear", "network list",
		"set geolocation", "set viewport",
		"terminal focus",
	} {
		if command == hidden {
			return false
		}
	}
	return true
}

func schemaCommand(command string) map[string]any {
	parts := strings.Fields(command)
	flags := schemaFlags(command)
	positionalArgs := schemaPositionalArgs(command)
	return map[string]any{
		"command":        command,
		"path":           parts,
		"aliases":        []string{},
		"argumentMode":   "parsed",
		"summary":        "Run " + command + " in the local CLI",
		"usage":          "everyapi " + command + " [flags]",
		"flags":          flags,
		"positionalArgs": positionalArgs,
		"examples":       []string{"everyapi " + command},
		"notes":          []string{},
	}
}

func schemaPositionalArgs(command string) []string {
	switch command {
	case "automations edit", "automations remove", "automations run", "automations show", "artifacts delete":
		return []string{"id"}
	case "artifacts share", "artifacts unshare", "artifacts update":
		return []string{"file"}
	case "emulator attach":
		return []string{"device"}
	case "emulator button":
		return []string{"name"}
	case "emulator gesture":
		return []string{"points"}
	case "emulator install":
		return []string{"path"}
	case "emulator launch":
		return []string{"package"}
	case "emulator permissions":
		return []string{"op", "package", "permission"}
	case "emulator rotate":
		return []string{"orientation"}
	case "emulator tap":
		return []string{"x", "y"}
	case "emulator type":
		return []string{"text"}
	case "file open", "file diff":
		return []string{"path"}
	case "skills get":
		return []string{"topic"}
	case "vm recipe doctor", "vm runtime create":
		return []string{"recipe-id"}
	case "vm runtime show", "vm runtime suspend", "vm runtime resume", "vm runtime cleanup", "vm runtime cancel", "vm runtime cleanup-info", "vm runtime forget":
		return []string{"id"}
	}
	if strings.HasPrefix(command, "linear ") {
		if strings.Contains(command, "search") {
			return []string{"query"}
		}
		if strings.HasSuffix(command, " issue") || strings.Contains(command, "assignee ") || strings.Contains(command, "attach") || strings.Contains(command, "comment add") || strings.Contains(command, "due-date ") || strings.Contains(command, "estimate ") || strings.Contains(command, "label ") || strings.Contains(command, "priority ") || strings.Contains(command, "relation ") || strings.Contains(command, "status ") || strings.HasSuffix(command, "save-issue") {
			return []string{"id"}
		}
	}
	return []string{}
}

func schemaFlags(command string) []string {
	flags := []string{"help", "json", "pairing-code", "environment"}
	add := func(values ...string) { flags = append(flags, values...) }
	switch {
	case command == "claude-teams":
		return []string{}
	case command == "account add":
		add("agent")
	case strings.HasPrefix(command, "agent hooks "):
		add("page")
	case strings.HasPrefix(command, "artifacts "):
		add("api-url")
		switch command {
		case "artifacts delete":
			add("id")
		case "artifacts list":
			add("cursor")
		case "artifacts share", "artifacts unshare", "artifacts update":
			add("file")
		}
	case command == "console" || command == "network":
		add("limit", "worktree", "page")
	case command == "download":
		add("selector", "path", "worktree", "page")
	case strings.HasPrefix(command, "clipboard "):
		if command == "clipboard write" {
			add("text")
		}
		add("worktree", "page")
	case strings.HasPrefix(command, "cookie "):
		switch command {
		case "cookie get":
			add("url", "worktree", "page")
		case "cookie delete":
			add("name", "domain", "url", "worktree", "page")
		case "cookie set":
			add("name", "value", "domain", "path", "secure", "httpOnly", "sameSite", "expires", "worktree", "page")
		}
	case strings.HasPrefix(command, "dialog "):
		if command == "dialog accept" {
			add("text")
		}
		add("worktree", "page")
	case strings.HasPrefix(command, "capture "):
		add("worktree", "page")
	case strings.HasPrefix(command, "intercept "):
		if command == "intercept enable" {
			add("patterns")
		}
		add("worktree", "page")
	case strings.HasPrefix(command, "mouse "):
		switch command {
		case "mouse move":
			add("x", "y")
		case "mouse wheel":
			add("dy", "dx")
		default:
			add("button")
		}
		add("worktree", "page")
	case strings.HasPrefix(command, "storage "):
		if strings.HasSuffix(command, "get") {
			add("key")
		} else if strings.HasSuffix(command, "set") {
			add("key", "value")
		}
		add("worktree", "page")
	case strings.HasPrefix(command, "environment "):
		if command == "environment add" {
			add("name", "page")
		} else {
			add("page")
		}
	case strings.HasPrefix(command, "file "):
		switch command {
		case "file open":
			add("path", "worktree")
		case "file diff":
			add("path", "staged", "worktree")
		case "file open-changed":
			add("mode", "worktree")
		}
	case command == "host list":
		add("page")
	case command == "highlight":
		add("selector", "worktree", "page")
	case command == "inserttext":
		add("text", "worktree", "page")
	case strings.HasPrefix(command, "set "):
		switch command {
		case "set device":
			add("name", "worktree", "page")
		case "set offline":
			add("state", "worktree", "page")
		case "set headers":
			add("headers", "worktree", "page")
		case "set credentials":
			add("user", "pass", "worktree", "page")
		case "set media":
			add("color-scheme", "reduced-motion", "worktree", "page")
		}
	case command == "serve":
		add("port", "pairing-address", "mobile-pairing", "no-pairing", "project-root", "recipe-json", "page")
	case strings.HasPrefix(command, "repo "):
		switch command {
		case "repo add":
			add("path")
		case "repo search-refs":
			add("repo", "query", "limit")
		case "repo set-base-ref":
			add("repo", "ref")
		case "repo show":
			add("repo")
		}
	case strings.HasPrefix(command, "vm "):
		switch command {
		case "vm recipe list":
			add("repo-path")
		case "vm recipe doctor":
			add("recipe-id", "repo-path", "provision", "connect")
		case "vm runtime create":
			add("recipe-id", "repo-path", "instance-id", "project-id", "workspace-id", "workspace-name", "repo-url", "branch")
		case "vm runtime show", "vm runtime suspend", "vm runtime resume", "vm runtime cleanup", "vm runtime cancel", "vm runtime cleanup-info":
			add("id")
		case "vm runtime forget":
			add("id", "force")
		}
	case strings.HasPrefix(command, "computer "):
		add(schemaComputerFlags(command)...)
	case strings.HasPrefix(command, "linear "):
		add(schemaLinearFlags(command)...)
	case strings.HasPrefix(command, "worktree "):
		switch command {
		case "worktree create":
			add("repo", "project", "host", "project-host-setup", "name", "agent", "prompt", "base-branch", "issue", "linear-issue", "comment", "setup", "parent-worktree", "no-parent", "run-hooks", "activate")
		case "worktree list":
			add("repo", "limit")
		case "worktree ps":
			add("limit")
		case "worktree set":
			add("worktree", "display-name", "issue", "linear-issue", "comment", "workspace-status", "parent-worktree", "no-parent")
		case "worktree rm":
			add("worktree", "force", "run-hooks")
		case "worktree current":
			return flags
		default:
			add("worktree")
		}
	case strings.HasPrefix(command, "terminal "):
		switch command {
		case "terminal list":
			add("worktree", "limit", "include-visual-layouts")
		case "terminal read":
			add("terminal", "cursor", "limit", "screen")
		case "terminal send":
			add("terminal", "text", "enter", "interrupt")
		case "terminal wait":
			add("terminal", "for", "timeout-ms")
		case "terminal create":
			add("worktree", "command", "title", "focus")
		case "terminal split":
			add("terminal", "direction", "command")
		case "terminal rename":
			add("terminal", "title")
		case "terminal close":
			add("terminal", "tab")
		case "terminal stop":
			add("worktree")
		default:
			add("terminal")
		}
	case command == "wait":
		add("selector", "timeout", "text", "url", "load", "fn", "state", "worktree", "page")
	case command == "goto":
		add("url", "worktree", "page")
	case command == "find":
		add("locator", "value", "action", "text", "worktree", "page")
	case command == "fill" || command == "select":
		add("element", "value", "worktree", "page")
	case command == "type":
		add("input", "worktree", "page")
	case command == "upload":
		add("element", "files", "worktree", "page")
	case command == "drag":
		add("from", "to", "worktree", "page")
	case command == "eval":
		add("expression", "worktree", "page")
	case command == "scroll":
		add("direction", "amount", "worktree", "page")
	case command == "keypress":
		add("key", "worktree", "page")
	case command == "get" || command == "is":
		add("what", "element", "worktree", "page")
	case command == "screenshot" || command == "full-screenshot":
		add("format", "worktree", "page")
	case command == "check" || command == "uncheck" || command == "focus" || command == "clear" || command == "click" || command == "dblclick" || command == "hover" || command == "scrollintoview":
		add("element", "worktree", "page")
	case command == "select-all":
		add("element", "worktree", "page")
	case command == "snapshot" || command == "back" || command == "forward" || command == "reload" || command == "pdf":
		add("worktree", "page")
	case command == "exec":
		add("command", "worktree", "page")
	case command == "viewport":
		add("width", "height", "scale", "mobile", "worktree", "page")
	case command == "geolocation":
		add("latitude", "longitude", "accuracy", "worktree", "page")
	case strings.HasPrefix(command, "tab "):
		add(schemaTabFlags(command)...)
	case strings.HasPrefix(command, "emulator "):
		add(schemaEmulatorFlags(command)...)
	case strings.HasPrefix(command, "orchestration "):
		add(schemaOrchestrationFlags(command)...)
	case strings.HasPrefix(command, "automations "):
		add(schemaAutomationFlags(command)...)
	case strings.HasPrefix(command, "project "):
		add(schemaProjectFlags(command)...)
	case strings.HasPrefix(command, "skills "):
		add(schemaSkillsFlags(command)...)
	}
	return flags
}

func schemaTabFlags(command string) []string {
	switch command {
	case "tab create":
		return []string{"url", "worktree", "profile"}
	case "tab list":
		return []string{"worktree", "show-profile"}
	case "tab close", "tab switch":
		if command == "tab close" {
			return []string{"index", "worktree", "page"}
		}
		return []string{"index", "page", "worktree", "focus"}
	case "tab show":
		return []string{"page", "worktree"}
	case "tab current":
		return []string{"worktree"}
	case "tab profile create":
		return []string{"label", "scope", "no-ua-spoof"}
	case "tab profile delete":
		return []string{"profile"}
	case "tab profile set":
		return []string{"profile", "page", "worktree"}
	case "tab profile show", "tab profile use-default":
		return []string{"page", "worktree"}
	case "tab profile clone":
		return []string{"profile", "page", "worktree"}
	default:
		return nil
	}
}

func schemaEmulatorFlags(command string) []string {
	base := []string{"device", "emulator", "worktree"}
	switch command {
	case "emulator attach":
		return []string{"worktree", "focus", "device"}
	case "emulator list", "emulator devices":
		return []string{"worktree"}
	case "emulator tap":
		return append(base, "x", "y")
	case "emulator type":
		return append([]string{"text"}, base...)
	case "emulator gesture":
		return append([]string{"points"}, base...)
	case "emulator button":
		return append(base, "name")
	case "emulator rotate":
		return append(base, "orientation")
	case "emulator exec":
		return append([]string{"command"}, base...)
	case "emulator install":
		return append(base, "path", "reinstall")
	case "emulator launch":
		return append(base, "package", "activity")
	case "emulator logcat":
		return append(base, "lines")
	case "emulator permissions":
		return append(base, "op", "package", "permission")
	default:
		return base
	}
}

func schemaAutomationFlags(command string) []string {
	control := []string{"control-state"}
	if command == "automations list" || command == "automations snapshot" {
		return control
	}
	if command == "automations show" || command == "automations remove" || command == "automations run" {
		return append([]string{"id"}, control...)
	}
	if command == "automations runs" {
		return append([]string{"id"}, control...)
	}
	flags := []string{"name", "clear-name", "prompt", "provider", "session", "precheck", "precheck-timeout", "precheck-approved", "precheck-approval", "repo", "workspace", "project", "host", "project-host-setup", "source-context", "workspace-mode", "base-branch", "trigger", "schedule", "every-seconds", "time", "day", "timezone", "enabled", "disabled", "missed-run-policy", "missed-run-grace-minutes", "reuse-session", "fresh-session", "control-state"}
	if command == "automations edit" {
		flags = append([]string{"id"}, flags...)
	}
	return flags
}

func schemaProjectFlags(command string) []string {
	switch command {
	case "project list":
		return nil
	case "project setups":
		return []string{"project", "host"}
	case "project setup-existing-folder":
		return []string{"project", "host", "path", "kind", "display-name"}
	case "project setup-clone":
		return []string{"project", "host", "url", "destination", "display-name"}
	case "project setup-create":
		return []string{"project", "host", "setup-id", "path", "kind", "display-name", "worktree-base-path", "git-username", "state", "method"}
	case "project setup-update":
		return []string{"setup", "display-name", "path", "worktree-base-path", "git-username", "kind", "state", "method"}
	case "project setup-delete":
		return []string{"setup"}
	}
	return nil
}

func schemaSkillsFlags(command string) []string {
	switch command {
	case "skills get":
		return []string{"topic", "full"}
	case "skills share":
		return []string{"skill", "bundle-name", "release-notes"}
	case "skills install":
		return []string{"skill", "all", "agent", "local", "dry-run"}
	case "skills update":
		return []string{"skill", "all", "local", "dry-run"}
	default:
		return nil
	}
}

func schemaOrchestrationFlags(command string) []string {
	switch command {
	case "orchestration run-create":
		return []string{"objective", "from", "retry-request"}
	case "orchestration run-use":
		return []string{"id", "from", "takeover-legacy", "retry-request"}
	case "orchestration run-current":
		return []string{"from"}
	case "orchestration run-list":
		return []string{"limit", "cursor"}
	case "orchestration run-show":
		return []string{"id"}
	case "orchestration send":
		return []string{"to", "run", "from", "subject", "body", "type", "priority", "thread-id", "payload", "task-id", "dispatch-id", "dispatch-capability", "retry-request", "outcome", "files-modified", "report-path", "phase"}
	case "orchestration ask":
		return []string{"to", "run", "question", "resume", "dispatch-capability", "options", "timeout-ms", "from", "retry-request"}
	case "orchestration check":
		return []string{"terminal", "run", "ack", "unread", "peek", "all", "types", "format", "wait", "timeout-ms", "retry-request"}
	case "orchestration inbox":
		return []string{"limit", "terminal", "full"}
	case "orchestration reply":
		return []string{"id", "body", "run", "from", "retry-request"}
	case "orchestration task-create":
		return []string{"spec", "task-title", "display-name", "deps", "parent", "run", "from", "retry-request"}
	case "orchestration task-list":
		return []string{"status", "ready", "brief", "run", "from"}
	case "orchestration task-update":
		return []string{"id", "status", "result", "run", "from", "retry-request"}
	case "orchestration dispatch":
		return []string{"task", "to", "from", "run", "inject", "dry-run", "return-preamble", "retry-request"}
	case "orchestration dispatch-show":
		return []string{"task", "preamble", "from"}
	case "orchestration worker-start":
		return []string{"task", "on", "worktree", "name", "repo", "base-branch", "display-name", "comment", "setup", "agent", "model", "effort", "terminal", "retry-of", "timeout-ms", "run", "from", "retry-request"}
	case "orchestration worker-show":
		return []string{"dispatch"}
	case "orchestration worker-stop", "orchestration worker-abandon", "orchestration worker-release", "orchestration worker-retain":
		return []string{"dispatch", "retry-request"}
	case "orchestration worker-read":
		return []string{"dispatch", "source", "cursor", "limit"}
	case "orchestration worker-list":
		return []string{"run", "terminal-state"}
	case "orchestration coordinator-start":
		return []string{"spec", "from", "poll-interval-ms", "max-concurrent", "worktree"}
	case "orchestration gate-create":
		return []string{"task", "question", "options", "from", "retry-request"}
	case "orchestration gate-resolve":
		return []string{"id", "resolution", "from", "retry-request"}
	case "orchestration gate-list":
		return []string{"task", "status", "run", "from"}
	case "orchestration reset":
		return []string{"all", "tasks", "messages", "retry-request"}
	}
	return nil
}

func schemaComputerFlags(command string) []string {
	if command == "computer capabilities" || command == "computer list-apps" {
		return []string{}
	}
	if command == "computer list-windows" {
		return []string{"app"}
	}
	if command == "computer permissions" {
		return []string{"id"}
	}
	base := []string{"worktree", "session", "app", "window-id", "window-index", "restore-window", "no-screenshot"}
	switch command {
	case "computer click":
		return append(base, "element-index", "x", "y", "click-count", "mouse-button", "modifiers")
	case "computer drag":
		return append(base, "from-element-index", "to-element-index", "from-x", "from-y", "to-x", "to-y")
	case "computer get-app-state":
		return base
	case "computer hotkey", "computer press-key":
		return append(base, "key")
	case "computer paste-text", "computer type-text":
		return append(base, "text", "text-stdin")
	case "computer perform-secondary-action":
		return append(base, "element-index", "action")
	case "computer scroll":
		return append(base, "element-index", "x", "y", "direction", "pages")
	case "computer set-value":
		return append(base, "element-index", "value", "value-stdin")
	}
	return base
}

func schemaLinearFlags(command string) []string {
	if command == "linear list" {
		return []string{"filter", "team", "limit", "workspace"}
	}
	if command == "linear list-issues" {
		return []string{"team", "cycle", "label", "limit", "query", "state", "cursor", "order-by", "project", "release", "assignee", "delegate", "parent-id", "priority", "created-at", "updated-at", "include-archived", "workspace"}
	}
	if command == "linear search" {
		return []string{"limit", "workspace", "query"}
	}
	if command == "linear create" {
		return []string{"title", "body", "body-file", "team", "project", "state", "assignee", "priority", "estimate", "due-date", "label", "parent", "parent-current", "write-id", "workspace"}
	}
	if command == "linear save-issue" {
		return []string{"current", "team", "title", "description", "body", "body-file", "state", "assignee", "priority", "estimate", "due-date", "label", "project", "parent-id", "write-id", "workspace", "id"}
	}
	if strings.HasPrefix(command, "linear team ") {
		if command == "linear team list" {
			return []string{"workspace"}
		}
		return []string{"team", "workspace"}
	}
	if command == "linear project list" {
		return []string{"query", "limit", "workspace"}
	}
	if command == "linear issue" {
		return []string{"current", "comments", "children", "depth", "attachments", "relations", "activity", "full", "workspace", "id"}
	}
	if command == "linear comment add" {
		return []string{"current", "body", "body-file", "reply-to", "write-id", "workspace", "id"}
	}
	if command == "linear attach" {
		return []string{"current", "url", "title", "write-id", "workspace", "id"}
	}
	if strings.HasPrefix(command, "linear assignee ") {
		if strings.HasSuffix(command, "set") {
			return []string{"current", "me", "to-id", "workspace", "id"}
		}
		return []string{"current", "workspace", "id"}
	}
	for _, field := range []string{"due-date", "estimate", "priority", "status"} {
		if strings.HasPrefix(command, "linear "+field+" ") {
			if strings.HasSuffix(command, "set") {
				return []string{"current", "to", "workspace", "id"}
			}
			return []string{"current", "workspace", "id"}
		}
	}
	if strings.HasPrefix(command, "linear label ") {
		return []string{"current", "label", "workspace", "id"}
	}
	if strings.HasPrefix(command, "linear relation ") {
		return []string{"current", "related", "type", "workspace", "id"}
	}
	return nil
}

var nativeCommandNames = []string{
	"open", "serve", "claude-teams", "status", "host", "repo", "worktree", "terminal", "account", "computer",
	"snapshot", "screenshot", "goto", "click", "fill", "type", "select", "scroll", "back", "reload", "eval", "wait",
	"check", "uncheck", "focus", "clear", "select-all", "keypress", "pdf", "full-screenshot", "hover", "drag", "upload", "tab",
	"exec", "cookie", "storage", "console", "network", "find", "clipboard", "dialog", "download", "highlight", "capture", "viewport",
	"geolocation", "intercept", "mouse", "inserttext", "is", "get", "scrollintoview", "dblclick", "forward", "set",
	"skills", "orchestration", "automations", "environment", "project", "file", "linear", "vm", "emulator", "agent", "diagnostics", "agent-context", "artifacts",
}

var nativeSubcommands = map[string][]string{
	"account":       {"add", "list"},
	"skills":        {"installed", "share", "list", "get", "install", "update"},
	"host":          {"list"},
	"environment":   {"add", "list", "show", "rm"},
	"vm":            {"recipe list", "recipe doctor", "runtime list", "runtime show", "runtime create", "runtime suspend", "runtime resume", "runtime cleanup", "runtime cancel", "runtime cleanup-info", "runtime forget"},
	"automations":   {"snapshot", "list", "show", "create", "edit", "remove", "run", "runs"},
	"project":       {"list", "setups", "setup-existing-folder", "setup-clone", "setup-create", "setup-update", "setup-delete"},
	"repo":          {"list", "add", "show", "set-base-ref", "search-refs"},
	"worktree":      {"list", "show", "current", "create", "set", "rm", "ps"},
	"file":          {"open", "diff", "open-changed"},
	"terminal":      {"list", "show", "read", "send", "wait", "stop", "create", "rename", "split", "switch", "focus", "close"},
	"orchestration": {"run-create", "run-use", "run-current", "run-list", "run-show", "send", "check", "ask", "reply", "inbox", "task-create", "task-list", "task-update", "dispatch", "dispatch-show", "worker-start", "worker-show", "worker-read", "worker-stop", "worker-abandon", "worker-release", "worker-retain", "worker-list", "coordinator-start", "coordinator-stop", "gate-create", "gate-resolve", "gate-list", "reset"},
	"emulator":      {"list", "devices", "attach", "tap", "type", "gesture", "button", "rotate", "exec", "kill", "shutdown", "ax", "install", "launch", "logcat", "permissions"},
	"tab":           {"create", "list", "show", "current", "profile list", "profile create", "profile delete", "profile set", "profile show", "profile use-default", "profile clone", "switch", "close"},
	"storage":       {"local get", "local set", "local clear", "session get", "session set", "session clear"},
	"cookie":        {"get", "set", "clear", "delete", "list"},
	"dialog":        {"accept", "dismiss", "get", "list"},
	"capture":       {"start", "stop", "status", "list"},
	"intercept":     {"add", "remove", "list", "enable", "disable"},
	"mouse":         {"move", "down", "up", "wheel"},
	"clipboard":     {"read", "write"},
	"download":      {"list", "save", "clear"},
	"console":       {"list", "clear"},
	"network":       {"list", "clear"},
	"linear":        {"issue", "search", "list", "list-issues", "create", "save-issue", "assignee clear", "assignee set", "attach", "comment add", "due-date clear", "due-date set", "estimate clear", "estimate set", "label add", "label remove", "label set", "priority clear", "priority set", "project list", "relation add", "relation remove", "status set", "team labels", "team list", "team members", "team states"},
	"set":           {"device", "offline", "headers", "credentials", "media", "viewport", "geolocation"},
	"computer":      {"capabilities", "permissions", "list-apps", "list-windows", "get-app-state", "click", "perform-secondary-action", "scroll", "drag", "type-text", "press-key", "hotkey", "paste-text", "set-value"},
	"agent":         {"hooks off", "hooks on", "hooks prepare-codex", "hooks status"},
	"diagnostics":   {"memory"},
	"artifacts":     {"delete", "list", "share", "unshare", "update"},
}

func orchestration(args []string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return map[string]any{"commands": []string{"run-create", "run-use", "run-current", "run-list", "run-show", "send", "check", "ask", "reply", "inbox", "task-create", "task-list", "task-update", "dispatch", "dispatch-show", "worker-start", "worker-show", "worker-read", "worker-stop", "worker-abandon", "worker-release", "worker-retain", "worker-list", "coordinator-start", "coordinator-stop", "gate-create", "gate-resolve", "gate-list", "reset"}}, nil
	}
	store, err := loadState("orchestration.json")
	if err != nil {
		return nil, err
	}
	switch args[0] {
	case "run-create":
		id := flagValue(args[1:], "id", "run-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		item := map[string]any{"id": id, "kind": "run", "name": flagValue(args[1:], "name", id), "status": "active", "createdAt": time.Now().UTC().Format(time.RFC3339)}
		applyOrchestrationFlags(item, args[1:], "objective", "from")
		store.Items = upsertStateItem(store.Items, id, item)
		store.Current = id
		return item, saveState("orchestration.json", store)
	case "run-use":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		if id == "" {
			return nil, errors.New("--id is required")
		}
		store.Current = id
		item := map[string]any{"run": id, "bound": true}
		applyOrchestrationFlags(item, args[1:], "from", "takeover-legacy")
		return item, saveState("orchestration.json", store)
	case "run-current":
		if store.Current == "" {
			store.Current = "local"
			_ = saveState("orchestration.json", store)
		}
		return map[string]any{"run": store.Current}, nil
	case "run-list", "inbox", "check":
		items := store.Items
		if args[0] == "run-list" {
			items = filterStateItems(items, "run")
		}
		if limit := intValue(args[1:], "limit", 0); limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		if args[0] == "inbox" && hasFlag(args[1:], "--full") {
			return map[string]any{"items": items, "full": true}, nil
		}
		return items, nil
	case "run-show":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		if id == "" {
			id = store.Current
		}
		if item, _ := findStateItem(store.Items, id); item != nil {
			return item, nil
		}
		return nil, fmt.Errorf("run %q not found", id)
	case "send":
		message := flagValueAny(args[1:], "body", "message")
		if message == "" {
			message = strings.Join(positional(args[1:]), " ")
		}
		if message == "" {
			return nil, errors.New("--body is required")
		}
		item := map[string]any{"id": "message-" + strconv.FormatInt(time.Now().UnixNano(), 10), "kind": "message", "run": store.Current, "message": message, "body": message, "time": time.Now().UTC().Format(time.RFC3339)}
		applyOrchestrationFlags(item, args[1:], "to", "run", "from", "subject", "type", "priority", "thread-id", "payload", "task-id", "dispatch-id", "dispatch-capability", "outcome", "files-modified", "report-path", "phase")
		store.Items = append(store.Items, item)
		return item, saveState("orchestration.json", store)
	case "ask":
		message := flagValueAny(args[1:], "question", "message")
		if message == "" {
			message = strings.Join(positional(args[1:]), " ")
		}
		if message == "" {
			return nil, errors.New("--question is required")
		}
		item := map[string]any{"id": "question-" + strconv.FormatInt(time.Now().UnixNano(), 10), "kind": "question", "run": store.Current, "message": message, "question": message, "status": "pending", "time": time.Now().UTC().Format(time.RFC3339)}
		applyOrchestrationFlags(item, args[1:], "to", "run", "resume", "dispatch-capability", "options", "from", "retry-request")
		store.Items = append(store.Items, item)
		return item, saveState("orchestration.json", store)
	case "reply":
		itemID := flagValue(args[1:], "id", firstPath(args[1:]))
		replyParts := positional(args[1:])
		replyFallback := ""
		if len(replyParts) > 1 {
			replyFallback = strings.Join(replyParts[1:], " ")
		}
		message := flagValueAny(args[1:], "body", "message")
		if message == "" {
			message = replyFallback
		}
		if itemID == "" || message == "" {
			return nil, errors.New("--id and --message are required")
		}
		item, _ := findStateItem(store.Items, itemID)
		if item == nil {
			return nil, fmt.Errorf("question %q not found", itemID)
		}
		item["reply"] = message
		item["body"] = message
		item["status"] = "replied"
		applyOrchestrationFlags(item, args[1:], "run", "from", "retry-request")
		return item, saveState("orchestration.json", store)
	case "task-create":
		id := flagValue(args[1:], "id", "task-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		item := map[string]any{"id": id, "kind": "task", "title": flagValueAny(args[1:], "task-title", "title"), "status": "pending", "run": store.Current, "createdAt": time.Now().UTC().Format(time.RFC3339)}
		if item["title"] == "" {
			item["title"] = strings.Join(positional(args[1:]), " ")
		}
		applyOrchestrationFlags(item, args[1:], "spec", "display-name", "deps", "parent", "run", "from", "retry-request")
		store.Items = upsertStateItem(store.Items, id, item)
		return item, saveState("orchestration.json", store)
	case "task-list":
		items := filterStateItems(store.Items, "task")
		if status := flagValue(args[1:], "status", ""); status != "" {
			items = filterStateItemsBy(items, "status", status)
		}
		if run := flagValue(args[1:], "run", ""); run != "" {
			items = filterStateItemsBy(items, "run", run)
		}
		return items, nil
	case "task-update":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		item, _ := findStateItem(store.Items, id)
		if item == nil {
			return nil, fmt.Errorf("task %q not found", id)
		}
		for _, field := range []string{"title", "status", "assignee", "comment", "result", "run", "from"} {
			if value := flagValue(args[1:], field, ""); value != "" {
				item[field] = value
			}
		}
		return item, saveState("orchestration.json", store)
	case "dispatch":
		id := flagValue(args[1:], "task", flagValue(args[1:], "id", firstPath(args[1:])))
		item, _ := findStateItem(store.Items, id)
		if item == nil {
			return nil, fmt.Errorf("task %q not found", id)
		}
		item["status"] = "dispatched"
		applyOrchestrationFlags(item, args[1:], "to", "from", "run", "inject", "dry-run", "return-preamble", "retry-request")
		item["terminal"] = flagValue(args[1:], "terminal", flagValue(args[1:], "to", ""))
		return item, saveState("orchestration.json", store)
	case "dispatch-show":
		id := flagValue(args[1:], "task", flagValue(args[1:], "id", firstPath(args[1:])))
		if item, _ := findStateItem(store.Items, id); item != nil {
			applyOrchestrationFlags(item, args[1:], "preamble", "from")
			return item, nil
		}
		return nil, fmt.Errorf("dispatch %q not found", id)
	case "worker-start":
		id := flagValue(args[1:], "id", "worker-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		command := flagValue(args[1:], "command", "")
		item := map[string]any{"id": id, "kind": "worker", "status": "running", "command": command, "task": flagValue(args[1:], "task", ""), "startedAt": time.Now().UTC().Format(time.RFC3339)}
		applyOrchestrationFlags(item, args[1:], "task", "on", "worktree", "name", "repo", "base-branch", "display-name", "comment", "setup", "agent", "model", "effort", "terminal", "retry-of", "timeout-ms", "run", "from", "retry-request")
		if command != "" {
			logPath, pathErr := statePath("worker-" + id + ".log")
			if pathErr != nil {
				return nil, pathErr
			}
			logFile, fileErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if fileErr != nil {
				return nil, fileErr
			}
			workerCommand := exec.Command("sh", "-c", command)
			if runtime.GOOS == "windows" {
				workerCommand = exec.Command("cmd", "/C", command)
			}
			workerCommand.Dir = mustGetwd()
			workerCommand.Stdout, workerCommand.Stderr = logFile, logFile
			if startErr := workerCommand.Start(); startErr != nil {
				_ = logFile.Close()
				return nil, startErr
			}
			_ = logFile.Close()
			item["pid"] = workerCommand.Process.Pid
			item["log"] = logPath
		}
		store.Items = upsertStateItem(store.Items, id, item)
		return item, saveState("orchestration.json", store)
	case "worker-list":
		for _, item := range store.Items {
			if item["kind"] == "worker" {
				refreshWorkerStatus(item)
			}
		}
		return filterStateItems(store.Items, "worker"), nil
	case "worker-show", "worker-read":
		id := flagValue(args[1:], "dispatch", flagValue(args[1:], "id", firstPath(args[1:])))
		if item, _ := findStateItem(store.Items, id); item != nil {
			refreshWorkerStatus(item)
			if args[0] == "worker-read" {
				if logPath, ok := item["log"].(string); ok && logPath != "" {
					if output, readErr := os.ReadFile(logPath); readErr == nil {
						item["output"] = string(output)
					}
				}
			}
			return item, nil
		}
		return nil, fmt.Errorf("worker %q not found", id)
	case "worker-stop", "worker-abandon", "worker-release", "worker-retain":
		id := flagValue(args[1:], "dispatch", flagValue(args[1:], "id", firstPath(args[1:])))
		item, _ := findStateItem(store.Items, id)
		if item == nil {
			return nil, fmt.Errorf("worker %q not found", id)
		}
		if pid := stateItemPID(item); pid > 0 && args[0] == "worker-stop" {
			if process, findErr := os.FindProcess(pid); findErr == nil {
				_ = process.Kill()
			}
		}
		workerStatus := map[string]string{"worker-stop": "stopped", "worker-abandon": "abandoned", "worker-release": "released", "worker-retain": "retained"}[args[0]]
		if workerStatus == "" {
			workerStatus = strings.TrimPrefix(args[0], "worker-")
		}
		item["status"] = workerStatus
		item["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
		return item, saveState("orchestration.json", store)
	case "coordinator-start", "coordinator-stop":
		item := map[string]any{"id": "coordinator", "kind": "coordinator", "status": strings.TrimPrefix(args[0], "coordinator-"), "updatedAt": time.Now().UTC().Format(time.RFC3339)}
		store.Items = upsertStateItem(store.Items, "coordinator", item)
		return item, saveState("orchestration.json", store)
	case "gate-create":
		id := flagValue(args[1:], "id", "gate-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		item := map[string]any{"id": id, "kind": "gate", "task": flagValue(args[1:], "task", ""), "question": flagValue(args[1:], "question", strings.Join(positional(args[1:]), " ")), "status": "pending", "createdAt": time.Now().UTC().Format(time.RFC3339)}
		applyOrchestrationFlags(item, args[1:], "options", "from", "retry-request")
		store.Items = upsertStateItem(store.Items, id, item)
		return item, saveState("orchestration.json", store)
	case "gate-resolve":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		item, _ := findStateItem(store.Items, id)
		if item == nil {
			return nil, fmt.Errorf("gate %q not found", id)
		}
		decisionParts := positional(args[1:])
		decisionFallback := ""
		if len(decisionParts) > 1 {
			decisionFallback = strings.Join(decisionParts[1:], " ")
		}
		item["status"] = "resolved"
		item["decision"] = flagValueAny(args[1:], "resolution", "decision")
		if item["decision"] == "" {
			item["decision"] = decisionFallback
		}
		applyOrchestrationFlags(item, args[1:], "from", "retry-request")
		return item, saveState("orchestration.json", store)
	case "gate-list":
		items := filterStateItems(store.Items, "gate")
		for _, field := range []string{"task", "status", "run", "from"} {
			if value := flagValue(args[1:], field, ""); value != "" {
				items = filterStateItemsBy(items, field, value)
			}
		}
		return items, nil
	case "reset":
		return map[string]any{"reset": true}, saveState("orchestration.json", state{})
	default:
		return nil, fmt.Errorf("unknown orchestration subcommand %q", args[0])
	}
}

func automations(args []string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return map[string]any{"commands": []string{"snapshot", "list", "show", "create", "edit", "remove", "run", "runs"}}, nil
	}
	controlState, useControl, err := automationControlStatePath(args)
	if err != nil {
		return nil, err
	}
	if useControl {
		return automationsThroughControl(args, controlState)
	}
	store, err := loadState("automations.json")
	if err != nil {
		return nil, err
	}
	switch args[0] {
	case "list":
		return store.Items, nil
	case "runs":
		runs, err := loadState("automation-runs.json")
		if err != nil {
			return nil, err
		}
		if id := flagValue(args[1:], "id", ""); id != "" {
			filtered := make([]map[string]any, 0, len(runs.Items))
			for _, item := range runs.Items {
				if fmt.Sprint(item["automationId"]) == id || fmt.Sprint(item["id"]) == id {
					filtered = append(filtered, item)
				}
			}
			return filtered, nil
		}
		return runs.Items, nil
	case "show":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		for _, item := range store.Items {
			if item["id"] == id {
				return item, nil
			}
		}
		return nil, fmt.Errorf("automation %q not found", id)
	case "create":
		id := flagValue(args[1:], "id", "automation-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		item := map[string]any{"id": id, "name": flagValue(args[1:], "name", id), "schedule": flagValue(args[1:], "schedule", "manual"), "command": flagValueAny(args[1:], "prompt", "command"), "enabled": true, "createdAt": time.Now().UTC().Format(time.RFC3339)}
		applyAutomationFlags(item, args[1:])
		store.Items = upsertStateItem(store.Items, id, item)
		return item, saveState("automations.json", store)
	case "edit", "update":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		if id == "" {
			return nil, errors.New("--id is required")
		}
		for _, item := range store.Items {
			if item["id"] == id {
				for _, field := range []string{"name", "schedule", "command", "enabled"} {
					if value := flagValue(args[1:], field, ""); value != "" {
						if field == "enabled" {
							parsed, parseErr := strconv.ParseBool(value)
							if parseErr != nil {
								return nil, fmt.Errorf("automation enabled must be true or false")
							}
							item[field] = parsed
						} else {
							item[field] = value
						}
					}
				}
				if prompt := flagValue(args[1:], "prompt", ""); prompt != "" {
					item["prompt"], item["command"] = prompt, prompt
				}
				applyAutomationFlags(item, args[1:])
				return item, saveState("automations.json", store)
			}
		}
		return nil, fmt.Errorf("automation %q not found", id)
	case "remove", "delete":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		for i, item := range store.Items {
			if item["id"] == id {
				store.Items = append(store.Items[:i], store.Items[i+1:]...)
				if err := saveState("automations.json", store); err != nil {
					return nil, err
				}
				return map[string]any{"id": id, "removed": true}, nil
			}
		}
		return nil, fmt.Errorf("automation %q not found", id)
	case "run":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		command := flagValueAny(args[1:], "prompt", "command", "")
		run := map[string]any{"id": id, "automationId": id, "started": true, "time": time.Now().UTC().Format(time.RFC3339)}
		if command != "" {
			cmd := shellCommand(command)
			cmd.Dir = mustGetwd()
			output, execErr := cmd.CombinedOutput()
			run["output"] = string(output)
			run["exitCode"] = 0
			if execErr != nil {
				var exitErr *exec.ExitError
				if errors.As(execErr, &exitErr) {
					run["exitCode"] = exitErr.ExitCode()
				}
				run["started"] = false
			}
		}
		runs, err := loadState("automation-runs.json")
		if err != nil {
			return nil, err
		}
		runs.Items = append(runs.Items, run)
		return run, saveState("automation-runs.json", runs)
	default:
		return nil, fmt.Errorf("unknown automations subcommand %q", args[0])
	}
}

func applyAutomationFlags(item map[string]any, args []string) {
	for _, field := range []string{"prompt", "provider", "precheck", "precheck-timeout", "repo", "workspace", "project", "host", "project-host-setup", "source-context", "workspace-mode", "base-branch", "trigger", "schedule", "time", "day", "timezone", "missed-run-grace-minutes"} {
		if value := flagValue(args, field, ""); value != "" {
			item[field] = value
		}
	}
	if prompt := flagValue(args, "prompt", ""); prompt != "" {
		item["command"] = prompt
	}
	if hasFlag(args, "--disabled") {
		item["enabled"] = false
	}
	if hasFlag(args, "--enabled") {
		item["enabled"] = true
	}
	if value := flagValue(args, "enabled", ""); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			item["enabled"] = parsed
		}
	}
	if hasFlag(args, "--reuse-session") {
		item["reuseSession"] = true
	}
	if hasFlag(args, "--fresh-session") {
		item["freshSession"] = true
	}
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-c", command)
}

type state struct {
	Current string           `json:"current,omitempty"`
	Items   []map[string]any `json:"items,omitempty"`
}

func upsertStateItem(items []map[string]any, key string, value map[string]any) []map[string]any {
	for i, item := range items {
		if item["path"] == key || item["id"] == key {
			items[i] = value
			return items
		}
	}
	return append(items, value)
}

func findStateItem(items []map[string]any, id string) (map[string]any, int) {
	for i, item := range items {
		if item["id"] == id || item["name"] == id {
			return item, i
		}
	}
	return nil, -1
}

func filterStateItems(items []map[string]any, kind string) []map[string]any {
	filtered := make([]map[string]any, 0)
	for _, item := range items {
		if item["kind"] == kind {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func filterStateItemsBy(items []map[string]any, field, value string) []map[string]any {
	filtered := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if fmt.Sprint(item[field]) == value {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func applyOrchestrationFlags(item map[string]any, args []string, fields ...string) {
	for _, field := range fields {
		if value := flagValue(args, field, ""); value != "" {
			if field == "options" || field == "payload" {
				var parsed any
				if json.Unmarshal([]byte(value), &parsed) == nil {
					item[field] = parsed
					continue
				}
			}
			item[field] = value
		}
	}
	for _, field := range []string{"dry-run", "return-preamble", "takeover-legacy", "retry-request", "inject", "ready", "full", "ack", "unread", "peek", "all"} {
		if hasFlag(args, "--"+field) {
			item[field] = true
		}
	}
}

func refreshWorkerStatus(item map[string]any) {
	pid := stateItemPID(item)
	if pid == 0 || !workerProcessAlive(pid) {
		if item["status"] == "running" {
			item["status"] = "exited"
		}
		item["alive"] = false
		return
	}
	item["alive"] = true
}

func stateItemPID(item map[string]any) int {
	switch value := item["pid"].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case uint:
		return int(value)
	case uint64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		pid, _ := value.Int64()
		return int(pid)
	default:
		return 0
	}
}

func workerProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func statePath(name string) (string, error) {
	dir := os.Getenv("EVERYAPI_WORKSPACE_STATE_DIR")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	if os.Getenv("EVERYAPI_WORKSPACE_STATE_DIR") == "" {
		dir = filepath.Join(dir, "everyapi")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func loadState(name string) (state, error) {
	path, err := statePath(name)
	if err != nil {
		return state{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state{}, nil
	}
	if err != nil {
		return state{}, err
	}
	var result state
	return result, json.Unmarshal(data, &result)
}

func saveState(name string, value state) error {
	path, err := statePath(name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func environment(args []string) (any, error) {
	store, err := loadState("environments.json")
	if err != nil {
		return nil, err
	}
	local := map[string]any{"id": "local", "name": "local", "os": runtime.GOOS, "online": true}
	if len(args) == 0 || isHelp(args[0]) || args[0] == "list" {
		items := []map[string]any{local}
		items = append(items, store.Items...)
		return items, nil
	}
	switch args[0] {
	case "show":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		if id == "" || id == "local" {
			return local, nil
		}
		for _, item := range store.Items {
			if item["id"] == id {
				return item, nil
			}
		}
		return nil, fmt.Errorf("environment %q not found", id)
	case "add":
		id := flagValue(args[1:], "id", "")
		if id == "" {
			id = "environment-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		}
		item := map[string]any{"id": id, "name": flagValue(args[1:], "name", id), "url": flagValue(args[1:], "url", ""), "online": true, "addedAt": time.Now().UTC().Format(time.RFC3339)}
		if code := flagValue(args[1:], "pairing-code", flagValue(args[1:], "code", "")); code != "" {
			item["pairingCodeSet"] = true
		}
		store.Items = upsertStateItem(store.Items, id, item)
		return item, saveState("environments.json", store)
	case "rm", "remove", "delete":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		if id == "" || id == "local" {
			return nil, errors.New("environment id is required and local cannot be removed")
		}
		for i, item := range store.Items {
			if item["id"] == id {
				store.Items = append(store.Items[:i], store.Items[i+1:]...)
				return map[string]any{"id": id, "removed": true}, saveState("environments.json", store)
			}
		}
		return nil, fmt.Errorf("environment %q not found", id)
	default:
		return nil, fmt.Errorf("unknown environment subcommand %q", args[0])
	}
}

func project(args []string) (any, error) {
	root, err := rootFromRepoFlag(args[1:])
	if err != nil {
		return nil, err
	}
	info, err := inspectRepo(root)
	if err != nil {
		return nil, err
	}
	store, err := loadState("project-setups.json")
	if err != nil {
		return nil, err
	}
	if len(args) == 0 || args[0] == "list" {
		if host := flagValue(args[1:], "host", ""); host != "" && host != "local" {
			return []repoInfo{}, nil
		}
		return []repoInfo{info}, nil
	}
	switch args[0] {
	case "setups":
		filtered := store.Items
		if projectID := flagValue(args[1:], "project", ""); projectID != "" {
			filtered = filterProjectSetups(filtered, projectID)
		}
		if host := flagValue(args[1:], "host", ""); host != "" {
			filtered = filterProjectSetups(filtered, host)
		}
		return filtered, nil
	case "setup-existing-folder":
		path := flagValue(args[1:], "path", firstPath(args[1:]))
		if path == "" {
			return nil, errors.New("--path is required")
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if stat, statErr := os.Stat(path); statErr != nil || !stat.IsDir() {
			return nil, fmt.Errorf("project folder is not a directory: %s", path)
		}
		id := flagValue(args[1:], "setup-id", flagValue(args[1:], "id", filepath.Base(path)))
		item := map[string]any{"id": id, "name": flagValueAny(args[1:], "display-name", "name", id), "path": path, "source": "existing-folder", "ready": true}
		applyProjectSetupFlags(item, args[1:])
		store.Items = upsertStateItem(store.Items, id, item)
		return item, saveState("project-setups.json", store)
	case "setup-clone":
		repository := flagValue(args[1:], "url", firstPath(args[1:]))
		if repository == "" {
			return nil, errors.New("--url is required")
		}
		path := flagValueAny(args[1:], "destination", "path")
		if path == "" {
			path = filepath.Join(filepath.Dir(root), filepath.Base(strings.TrimSuffix(repository, ".git")))
		}
		if _, cloneErr := commandOutput(filepath.Dir(path), "git", "clone", repository, path); cloneErr != nil {
			return nil, cloneErr
		}
		id := flagValue(args[1:], "setup-id", filepath.Base(path))
		item := map[string]any{"id": id, "name": flagValueAny(args[1:], "display-name", "name", id), "path": path, "repository": repository, "source": "clone", "ready": true}
		applyProjectSetupFlags(item, args[1:])
		store.Items = upsertStateItem(store.Items, id, item)
		return item, saveState("project-setups.json", store)
	case "setup-create":
		id := flagValue(args[1:], "setup-id", flagValue(args[1:], "id", "setup-"+strconv.FormatInt(time.Now().UnixNano(), 10)))
		item := map[string]any{"id": id, "name": flagValueAny(args[1:], "display-name", "name", id), "host": flagValue(args[1:], "host", "local"), "path": flagValue(args[1:], "path", ""), "ready": false, "createdAt": time.Now().UTC().Format(time.RFC3339)}
		applyProjectSetupFlags(item, args[1:])
		store.Items = upsertStateItem(store.Items, id, item)
		return item, saveState("project-setups.json", store)
	case "setup-update":
		id := flagValue(args[1:], "setup", flagValue(args[1:], "id", firstPath(args[1:])))
		if id == "" {
			return nil, errors.New("--id is required")
		}
		for _, item := range store.Items {
			if item["id"] == id {
				for _, field := range []string{"name", "host", "path", "repository", "ready", "display-name", "worktree-base-path", "git-username", "kind", "state", "method"} {
					if value := flagValue(args[1:], field, ""); value != "" {
						if field == "ready" {
							parsed, parseErr := strconv.ParseBool(value)
							if parseErr != nil {
								return nil, fmt.Errorf("project setup ready must be true or false")
							}
							item[field] = parsed
						} else {
							item[field] = value
						}
					}
				}
				if display := flagValue(args[1:], "display-name", ""); display != "" {
					item["name"] = display
				}
				return item, saveState("project-setups.json", store)
			}
		}
		return nil, fmt.Errorf("project setup %q not found", id)
	case "setup-delete", "setup-rm":
		id := flagValue(args[1:], "setup", flagValue(args[1:], "id", firstPath(args[1:])))
		if id == "" {
			return nil, errors.New("--id is required")
		}
		for i, item := range store.Items {
			if item["id"] == id {
				store.Items = append(store.Items[:i], store.Items[i+1:]...)
				return map[string]any{"id": id, "deleted": true}, saveState("project-setups.json", store)
			}
		}
		return nil, fmt.Errorf("project setup %q not found", id)
	default:
		return nil, fmt.Errorf("unknown project subcommand %q", args[0])
	}
}

func applyProjectSetupFlags(item map[string]any, args []string) {
	for _, field := range []string{"project", "host", "kind", "display-name", "worktree-base-path", "git-username", "state", "method"} {
		if value := flagValue(args, field, ""); value != "" {
			item[field] = value
		}
	}
	if display := flagValue(args, "display-name", ""); display != "" {
		item["name"] = display
	}
}

func filterProjectSetups(items []map[string]any, selector string) []map[string]any {
	filtered := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if fmt.Sprint(item["id"]) == selector || fmt.Sprint(item["name"]) == selector || fmt.Sprint(item["project"]) == selector || fmt.Sprint(item["host"]) == selector {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func vm(args []string) (any, error) { return vmCommand(args) }

func emulator(args []string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return map[string]any{"commands": []string{"list", "devices", "attach", "tap", "type", "gesture", "button", "rotate", "exec", "kill", "shutdown", "ax", "install", "launch", "logcat", "permissions"}}, nil
	}
	store, err := loadState("emulators.json")
	if err != nil {
		return nil, err
	}
	if len(args) == 0 || args[0] == "list" || args[0] == "devices" {
		if runtime.GOOS == "darwin" {
			if _, bridgeErr := exec.LookPath("xcrun"); bridgeErr == nil {
				if out, commandErr := commandOutput(".", "xcrun", "simctl", "list", "devices", "available", "-j"); commandErr == nil {
					var data any
					if json.Unmarshal(out, &data) == nil {
						return data, nil
					}
				}
			}
		}
		if _, bridgeErr := exec.LookPath("adb"); bridgeErr == nil {
			if out, commandErr := commandOutput(".", "adb", "devices", "-l"); commandErr == nil {
				return string(out), nil
			}
		}
		return map[string]any{"bridge": "local", "devices": store.Items}, nil
	}
	sub := args[0]
	deviceID := flagValueAny(args[1:], "device", "emulator", "id")
	if deviceID == "" {
		deviceID = store.Current
	}
	if deviceID == "" && len(store.Items) > 0 {
		deviceID, _ = store.Items[0]["id"].(string)
	}
	if sub == "attach" {
		if deviceID == "" {
			return nil, errors.New("emulator attach requires a device argument or --device")
		}
		found := false
		for _, device := range store.Items {
			if device["id"] == deviceID {
				device["state"] = "attached"
				found = true
			}
		}
		if !found {
			store.Items = append(store.Items, map[string]any{"id": deviceID, "name": deviceID, "state": "attached", "actions": []map[string]any{}})
		}
		store.Current = deviceID
		bridge := "local"
		if _, bridgeErr := exec.LookPath("adb"); bridgeErr == nil {
			if _, commandErr := commandOutput(".", "adb", "-s", deviceID, "get-state"); commandErr == nil {
				bridge = "adb"
			}
		} else if runtime.GOOS == "darwin" {
			if _, bridgeErr := exec.LookPath("xcrun"); bridgeErr == nil {
				if _, commandErr := commandOutput(".", "xcrun", "simctl", "bootstatus", deviceID, "-b"); commandErr == nil {
					bridge = "simctl"
				}
			}
		}
		if err := saveState("emulators.json", store); err != nil {
			return nil, err
		}
		result := map[string]any{"id": deviceID, "attached": true, "bridge": bridge}
		if hasFlag(args[1:], "--focus") {
			result["focused"] = true
		}
		if worktree := flagValue(args[1:], "worktree", ""); worktree != "" {
			result["worktree"] = worktree
		}
		return result, nil
	}
	if sub == "kill" || sub == "shutdown" {
		if deviceID == "" {
			return nil, errors.New("emulator kill requires --id or --device")
		}
		for _, device := range store.Items {
			if device["id"] == deviceID {
				device["state"] = "stopped"
			}
		}
		if store.Current == deviceID {
			store.Current = ""
		}
		if err := saveState("emulators.json", store); err != nil {
			return nil, err
		}
		return map[string]any{"id": deviceID, "killed": true, "shutdown": sub == "shutdown"}, nil
	}
	if deviceID == "" {
		return nil, errors.New("an attached emulator is required (use --device)")
	}
	var target map[string]any
	for _, device := range store.Items {
		if device["id"] == deviceID {
			target = device
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("emulator %q is not attached", deviceID)
	}
	if target["actions"] == nil {
		target["actions"] = []map[string]any{}
	}
	actions, _ := target["actions"].([]any)
	appendAction := func(action map[string]any) {
		actions = append(actions, action)
		target["actions"] = actions
	}
	entry := map[string]any{"command": sub, "time": time.Now().UTC().Format(time.RFC3339)}
	var actionErr error
	switch sub {
	case "ax":
		return map[string]any{"id": deviceID, "accessibility": target["accessibility"], "actions": actions}, nil
	case "install":
		apk := flagValue(args[1:], "path", firstPath(args[1:]))
		if apk == "" {
			return nil, errors.New("emulator install requires an APK path")
		}
		info, statErr := os.Stat(apk)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("emulator install path is not a regular file: %s", apk)
		}
		entry["path"] = apk
		entry["reinstall"] = hasFlag(args[1:], "--reinstall")
		if _, bridgeErr := exec.LookPath("adb"); bridgeErr == nil {
			adbArgs := []string{"-s", deviceID, "install"}
			if entry["reinstall"].(bool) {
				adbArgs = append(adbArgs, "-r")
			}
			adbArgs = append(adbArgs, apk)
			if out, commandErr := commandOutput(".", "adb", adbArgs...); commandErr == nil {
				entry["output"] = string(out)
			} else {
				entry["error"] = commandErr.Error()
				actionErr = commandErr
			}
		}
		installed, _ := target["installed"].([]any)
		installed = append(installed, apk)
		target["installed"] = installed
	case "launch":
		pkg := flagValue(args[1:], "package", firstPath(args[1:]))
		if pkg == "" {
			return nil, errors.New("emulator launch requires a package")
		}
		activity := flagValue(args[1:], "activity", "")
		entry["package"] = pkg
		if activity != "" {
			entry["activity"] = activity
		}
		if _, bridgeErr := exec.LookPath("adb"); bridgeErr == nil {
			adbArgs := []string{"-s", deviceID, "shell", "monkey", "-p", pkg, "1"}
			if activity != "" {
				adbArgs = []string{"-s", deviceID, "shell", "am", "start", "-n", pkg + "/" + activity}
			}
			if out, commandErr := commandOutput(".", "adb", adbArgs...); commandErr == nil {
				entry["output"] = string(out)
			} else {
				entry["error"] = commandErr.Error()
				actionErr = commandErr
			}
		}
	case "logcat":
		lines := intValue(args[1:], "lines", 100)
		if _, bridgeErr := exec.LookPath("adb"); bridgeErr == nil {
			// Verify the target is online before asking adb for logcat. Some adb
			// builds keep a missing transport open indefinitely even with -d.
			if _, stateErr := commandOutput(".", "adb", "-s", deviceID, "get-state"); stateErr == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "adb", "-s", deviceID, "logcat", "-d", "-t", strconv.Itoa(lines))
				if out, commandErr := cmd.Output(); commandErr == nil {
					return map[string]any{"id": deviceID, "lines": lines, "output": string(out)}, nil
				}
			}
		}
		logs, _ := target["logs"].([]any)
		if len(logs) > lines {
			logs = logs[len(logs)-lines:]
		}
		return map[string]any{"id": deviceID, "lines": lines, "logs": logs}, nil
	case "permissions":
		op := flagValue(args[1:], "op", firstPath(args[1:]))
		if op == "reset" {
			target["permissions"] = map[string]any{}
			entry["operation"] = op
		} else {
			parts := positional(args[1:])
			pkg := flagValue(args[1:], "package", "")
			permission := flagValue(args[1:], "permission", "")
			if pkg != "" || permission != "" || flagValue(args[1:], "op", "") != "" {
				parts = []string{op, pkg, permission}
			}
			if len(parts) < 3 || (parts[0] != "grant" && parts[0] != "revoke") {
				return nil, errors.New("emulator permissions requires grant|revoke package permission or reset")
			}
			entry["operation"], entry["package"], entry["permission"] = parts[0], parts[1], parts[2]
			perms, _ := target["permissions"].(map[string]any)
			if perms == nil {
				perms = map[string]any{}
			}
			key := parts[1] + ":" + parts[2]
			if parts[0] == "grant" {
				perms[key] = true
			} else {
				delete(perms, key)
			}
			target["permissions"] = perms
		}
	case "tap":
		coords := positional(args[1:])
		xValue, yValue := flagValue(args[1:], "x", ""), flagValue(args[1:], "y", "")
		if xValue != "" || yValue != "" {
			coords = []string{xValue, yValue}
		}
		if len(coords) < 2 {
			return nil, errors.New("emulator tap requires x and y coordinates")
		}
		x, xErr := strconv.ParseFloat(coords[0], 64)
		y, yErr := strconv.ParseFloat(coords[1], 64)
		if xErr != nil || yErr != nil || x < 0 || x > 1 || y < 0 || y > 1 {
			return nil, errors.New("tap coordinates must be normalized numbers from 0 to 1")
		}
		entry["x"], entry["y"] = x, y
	case "type":
		value := flagValue(args[1:], "text", strings.Join(positional(args[1:]), " "))
		if value == "" {
			return nil, errors.New("emulator type requires text")
		}
		entry["text"] = value
	case "gesture":
		value := flagValue(args[1:], "points", flagValue(args[1:], "json", firstPath(args[1:])))
		var points any
		if err := json.Unmarshal([]byte(value), &points); err != nil {
			return nil, fmt.Errorf("gesture expects JSON points: %w", err)
		}
		entry["points"] = points
	case "button":
		button := flagValue(args[1:], "name", firstPath(args[1:]))
		if button == "" {
			return nil, errors.New("emulator button requires a name")
		}
		entry["name"] = button
	case "rotate":
		orientation := flagValue(args[1:], "orientation", firstPath(args[1:]))
		if orientation != "portrait" && orientation != "landscape_left" && orientation != "landscape_right" && orientation != "portrait_upside_down" {
			return nil, errors.New("orientation must be portrait, landscape_left, landscape_right, or portrait_upside_down")
		}
		entry["orientation"] = orientation
	case "exec":
		command := flagValue(args[1:], "command", strings.Join(positional(args[1:]), " "))
		if command == "" {
			return nil, errors.New("emulator exec requires --command")
		}
		entry["commandLine"] = command
	default:
		return nil, fmt.Errorf("unknown emulator subcommand %q", sub)
	}
	if worktree := flagValue(args[1:], "worktree", ""); worktree != "" {
		entry["worktree"] = worktree
	}
	if hasFlag(args[1:], "--focus") {
		entry["focus"] = true
	}
	appendAction(entry)
	if err := saveState("emulators.json", store); err != nil {
		return nil, err
	}
	return map[string]any{"id": deviceID, "action": entry}, actionErr
}

func localLinear(args []string) (any, error) {
	store, err := loadState("issues.json")
	if err != nil {
		return nil, err
	}
	if len(args) == 0 || isHelp(args[0]) {
		return map[string]any{"commands": []string{"issue", "search", "list", "list-issues", "create", "save-issue", "assignee", "comment", "due-date", "estimate", "label", "priority", "project", "relation", "status", "team"}, "issues": store.Items}, nil
	}
	findIssue := func(raw string, current bool) (map[string]any, int, error) {
		if raw == "" && current {
			raw = store.Current
		}
		if raw == "" {
			return nil, -1, errors.New("an issue id is required (or use --current)")
		}
		for i, item := range store.Items {
			if fmt.Sprint(item["id"]) == raw || fmt.Sprint(item["identifier"]) == raw {
				return item, i, nil
			}
		}
		return nil, -1, fmt.Errorf("issue %q not found in local cache", raw)
	}
	readBody := func(values []string) (string, error) {
		if body := flagValue(values, "body", ""); body != "" {
			return body, nil
		}
		if description := flagValue(values, "description", ""); description != "" {
			return description, nil
		}
		if path := flagValue(values, "body-file", ""); path != "" {
			if path == "-" {
				data, readErr := io.ReadAll(os.Stdin)
				return string(data), readErr
			}
			data, readErr := os.ReadFile(path)
			return string(data), readErr
		}
		return "", nil
	}
	applyFields := func(item map[string]any, values []string, create bool) error {
		if title := flagValue(values, "title", ""); title != "" {
			item["title"] = title
		} else if create {
			return errors.New("linear create requires --title")
		}
		if body, bodyErr := readBody(values); bodyErr != nil {
			return bodyErr
		} else if body != "" {
			item["body"], item["description"] = body, body
		}
		for _, field := range []string{"team", "project", "state", "assignee", "priority", "estimate", "due-date", "parent", "parent-id"} {
			if value := flagValue(values, field, ""); value != "" {
				if value == "null" {
					delete(item, field)
				} else {
					item[field] = value
				}
			}
		}
		if assignee := flagValue(values, "assignee", ""); assignee == "null" {
			delete(item, "assignee")
		}
		if priority := flagValue(values, "priority", ""); priority != "" && !validLinearPriority(priority) {
			return fmt.Errorf("invalid priority %q", priority)
		}
		if estimate := flagValue(values, "estimate", ""); estimate != "" {
			if _, e := strconv.ParseFloat(estimate, 64); e != nil {
				return fmt.Errorf("invalid estimate %q", estimate)
			}
		}
		if due := flagValue(values, "due-date", ""); due != "" && due != "null" {
			if _, e := time.Parse("2006-01-02", due); e != nil {
				return fmt.Errorf("invalid due date %q", due)
			}
		}
		if labels := flagValues(values, "label"); len(labels) > 0 {
			item["labels"] = stringsToAny(labels)
		}
		item["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
		return nil
	}
	switch args[0] {
	case "list":
		return store.Items, nil
	case "list-issues":
		items := append([]map[string]any(nil), store.Items...)
		query := strings.ToLower(flagValue(args[1:], "query", ""))
		team, stateName, label := flagValue(args[1:], "team", ""), flagValue(args[1:], "state", ""), flagValue(args[1:], "label", "")
		project, assignee, parentID := flagValue(args[1:], "project", ""), flagValue(args[1:], "assignee", ""), flagValue(args[1:], "parent-id", "")
		priorityFilter := flagValue(args[1:], "priority", "")
		filtered := items[:0]
		for _, item := range items {
			encoded, _ := json.Marshal(item)
			if query != "" && !strings.Contains(strings.ToLower(string(encoded)), query) {
				continue
			}
			if team != "" && fmt.Sprint(item["team"]) != team {
				continue
			}
			if stateName != "" && fmt.Sprint(item["state"]) != stateName {
				continue
			}
			if label != "" && !strings.Contains(fmt.Sprint(item["labels"]), label) {
				continue
			}
			if project != "" && fmt.Sprint(item["project"]) != project {
				continue
			}
			if assignee != "" {
				if assignee == "null" && item["assignee"] != nil {
					continue
				}
				if assignee != "null" && fmt.Sprint(item["assignee"]) != assignee && !(assignee == "me" && fmt.Sprint(item["assignee"]) == "me") {
					continue
				}
			}
			if parentID != "" && fmt.Sprint(item["parent-id"]) != parentID && fmt.Sprint(item["parent"]) != parentID {
				continue
			}
			if priorityFilter != "" && fmt.Sprint(item["priority"]) != priorityFilter {
				continue
			}
			filtered = append(filtered, item)
		}
		if order := flagValue(args[1:], "order-by", ""); order == "createdAt" || order == "updatedAt" {
			sort.SliceStable(filtered, func(i, j int) bool { return fmt.Sprint(filtered[i][order]) < fmt.Sprint(filtered[j][order]) })
		}
		if limit := intValue(args[1:], "limit", 0); limit > 0 && len(filtered) > limit {
			filtered = filtered[:limit]
		}
		return filtered, nil
	case "search":
		query := strings.ToLower(flagValue(args[1:], "query", strings.Join(positional(args[1:]), " ")))
		if query == "" {
			return store.Items, nil
		}
		var found []map[string]any
		for _, item := range store.Items {
			encoded, _ := json.Marshal(item)
			if strings.Contains(strings.ToLower(string(encoded)), query) {
				found = append(found, item)
			}
		}
		return found, nil
	case "issue":
		id := flagValue(args[1:], "id", firstPath(args[1:]))
		item, _, findErr := findIssue(id, hasFlag(args[1:], "--current"))
		return item, findErr
	case "create":
		id := "issue-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		item := map[string]any{"id": id, "identifier": "LOCAL-" + strconv.FormatInt(time.Now().UnixNano()%100000, 10), "status": "backlog", "createdAt": time.Now().UTC().Format(time.RFC3339)}
		if applyErr := applyFields(item, args[1:], true); applyErr != nil {
			return nil, applyErr
		}
		store.Items = append(store.Items, item)
		store.Current = id
		return item, saveState("issues.json", store)
	case "save-issue":
		values := args[1:]
		id := flagValue(values, "id", firstPath(values))
		current := hasFlag(values, "--current")
		item, index, findErr := findIssue(id, current)
		if findErr != nil {
			if id != "" || current {
				return nil, findErr
			}
			item = map[string]any{"id": "issue-" + strconv.FormatInt(time.Now().UnixNano(), 36), "identifier": "LOCAL-" + strconv.FormatInt(time.Now().UnixNano()%100000, 10), "status": "backlog", "createdAt": time.Now().UTC().Format(time.RFC3339)}
			if flagValue(values, "team", "") == "" || flagValue(values, "title", "") == "" {
				return nil, errors.New("linear save-issue requires --team and --title when creating")
			}
			index = -1
		}
		if applyErr := applyFields(item, values, index == -1); applyErr != nil {
			return nil, applyErr
		}
		if index == -1 {
			store.Items = append(store.Items, item)
		} else {
			store.Items[index] = item
		}
		store.Current = fmt.Sprint(item["id"])
		return item, saveState("issues.json", store)
	case "assignee", "due-date", "estimate", "priority", "status":
		if len(args) < 2 {
			return nil, fmt.Errorf("linear %s requires set or clear", args[0])
		}
		op := args[1]
		values := args[2:]
		item, index, findErr := findIssue(flagValue(values, "id", firstPath(values)), hasFlag(values, "--current"))
		if findErr != nil {
			return nil, findErr
		}
		field := args[0]
		if op == "clear" {
			delete(item, field)
		} else if op == "set" {
			value := flagValue(values, "to", "")
			if field == "assignee" {
				if hasFlag(values, "--me") {
					value = "me"
				}
				if value == "" {
					value = flagValue(values, "to-id", "")
				}
			}
			if value == "" {
				return nil, fmt.Errorf("linear %s set requires --to", field)
			}
			if field == "priority" && !validLinearPriority(value) {
				return nil, fmt.Errorf("invalid priority %q", value)
			}
			item[field] = value
		} else {
			return nil, fmt.Errorf("unknown linear %s operation %q", field, op)
		}
		item["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
		store.Items[index] = item
		return item, saveState("issues.json", store)
	case "label":
		if len(args) < 2 || (args[1] != "add" && args[1] != "remove" && args[1] != "set") {
			return nil, errors.New("linear label requires add, remove, or set")
		}
		values := args[2:]
		item, index, findErr := findIssue(flagValue(values, "id", firstPath(values)), hasFlag(values, "--current"))
		if findErr != nil {
			return nil, findErr
		}
		labels := flagValues(values, "label")
		if len(labels) == 0 {
			return nil, errors.New("linear label requires --label")
		}
		if args[1] == "set" {
			item["labels"] = stringsToAny(labels)
		} else {
			existing := anyStrings(item["labels"])
			if args[1] == "add" {
				existing = appendUnique(existing, labels...)
			} else {
				existing = removeValues(existing, labels)
			}
			item["labels"] = stringsToAny(existing)
		}
		store.Items[index] = item
		return item, saveState("issues.json", store)
	case "comment":
		if len(args) < 2 || args[1] != "add" {
			return nil, errors.New("linear comment requires add")
		}
		values := args[2:]
		item, index, findErr := findIssue(flagValue(values, "id", firstPath(values)), hasFlag(values, "--current"))
		if findErr != nil {
			return nil, findErr
		}
		body, bodyErr := readBody(values)
		if bodyErr != nil || body == "" {
			if bodyErr != nil {
				return nil, bodyErr
			}
			return nil, errors.New("linear comment add requires --body or --body-file")
		}
		comments, _ := item["comments"].([]any)
		comments = append(comments, map[string]any{"body": body, "createdAt": time.Now().UTC().Format(time.RFC3339), "replyTo": flagValue(values, "reply-to", "")})
		item["comments"] = comments
		store.Items[index] = item
		return item, saveState("issues.json", store)
	case "attach":
		values := args[1:]
		item, index, findErr := findIssue(flagValue(values, "id", firstPath(values)), hasFlag(values, "--current"))
		if findErr != nil {
			return nil, findErr
		}
		link := flagValue(values, "url", "")
		if link == "" {
			return nil, errors.New("linear attach requires --url")
		}
		attachments, _ := item["attachments"].([]any)
		attachments = append(attachments, map[string]any{"url": link, "title": flagValue(values, "title", "")})
		item["attachments"] = attachments
		store.Items[index] = item
		return item, saveState("issues.json", store)
	case "relation":
		if len(args) < 2 || (args[1] != "add" && args[1] != "remove") {
			return nil, errors.New("linear relation requires add or remove")
		}
		values := args[2:]
		item, index, findErr := findIssue(flagValue(values, "id", firstPath(values)), hasFlag(values, "--current"))
		if findErr != nil {
			return nil, findErr
		}
		related, relationType := flagValue(values, "related", ""), flagValue(values, "type", "")
		if related == "" || relationType == "" {
			return nil, errors.New("linear relation requires --related and --type")
		}
		switch relationType {
		case "blocks", "blocked-by", "related", "duplicate-of":
		default:
			return nil, fmt.Errorf("invalid relation type %q", relationType)
		}
		relations, _ := item["relations"].([]any)
		relation := map[string]any{"issue": related, "type": relationType}
		if args[1] == "add" {
			relations = append(relations, relation)
		} else {
			kept := relations[:0]
			for _, raw := range relations {
				m, _ := raw.(map[string]any)
				if fmt.Sprint(m["issue"]) != related || fmt.Sprint(m["type"]) != relationType {
					kept = append(kept, raw)
				}
			}
			relations = kept
		}
		item["relations"] = relations
		store.Items[index] = item
		return item, saveState("issues.json", store)
	case "team":
		if len(args) < 2 {
			return nil, errors.New("linear team requires list, labels, members, or states")
		}
		values := args[2:]
		team := flagValue(values, "team", "")
		if team == "" {
			team = "LOCAL"
		}
		switch args[1] {
		case "list":
			return []map[string]any{{"id": "local", "key": team, "name": "Local"}}, nil
		case "labels":
			return []map[string]any{{"id": "local-label", "name": "local"}}, nil
		case "members":
			return []map[string]any{{"id": "me", "name": "Local user"}}, nil
		case "states":
			return []map[string]any{{"id": "backlog", "name": "Backlog"}, {"id": "started", "name": "Started"}, {"id": "completed", "name": "Completed"}}, nil
		default:
			return nil, fmt.Errorf("unknown linear team operation %q", args[1])
		}
	case "project":
		if len(args) < 2 || args[1] != "list" {
			return nil, errors.New("linear project requires list")
		}
		query := strings.ToLower(flagValue(args[2:], "query", ""))
		projects := []map[string]any{}
		seen := map[string]bool{}
		for _, item := range store.Items {
			rawName, ok := item["project"]
			if !ok || rawName == nil {
				continue
			}
			name := strings.TrimSpace(fmt.Sprint(rawName))
			if name == "" || seen[name] || (query != "" && !strings.Contains(strings.ToLower(name), query)) {
				continue
			}
			seen[name] = true
			projects = append(projects, map[string]any{"id": name, "name": name})
		}
		if limit := intValue(args[2:], "limit", 0); limit > 0 && len(projects) > limit {
			projects = projects[:limit]
		}
		return projects, nil
	default:
		return nil, fmt.Errorf("unknown linear subcommand %q", args[0])
	}
}

func validLinearPriority(value string) bool {
	switch strings.ToLower(value) {
	case "none", "low", "medium", "high", "urgent":
		return true
	default:
		return false
	}
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}
func anyStrings(value any) []string {
	var result []string
	if values, ok := value.([]any); ok {
		for _, item := range values {
			result = append(result, fmt.Sprint(item))
		}
	}
	return result
}
func appendUnique(values []string, additions ...string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if !seen[value] {
			values = append(values, value)
			seen[value] = true
		}
	}
	return values
}
func removeValues(values, removals []string) []string {
	drop := map[string]bool{}
	for _, value := range removals {
		drop[value] = true
	}
	result := values[:0]
	for _, value := range values {
		if !drop[value] {
			result = append(result, value)
		}
	}
	return result
}

type browserState struct {
	URL          string                    `json:"url"`
	Title        string                    `json:"title,omitempty"`
	Text         string                    `json:"text,omitempty"`
	HTML         string                    `json:"html,omitempty"`
	History      []string                  `json:"history,omitempty"`
	HistoryIndex int                       `json:"historyIndex"`
	Fields       map[string]string         `json:"fields,omitempty"`
	Storage      map[string]string         `json:"storage,omitempty"`
	SessionStore map[string]string         `json:"sessionStorage,omitempty"`
	Cookies      map[string]string         `json:"cookies,omitempty"`
	CookieMeta   map[string]map[string]any `json:"cookieMeta,omitempty"`
	Console      []string                  `json:"console,omitempty"`
	Network      []map[string]any          `json:"network,omitempty"`
	Downloads    []string                  `json:"downloads,omitempty"`
	ScrollY      int                       `json:"scrollY,omitempty"`
	Viewport     string                    `json:"viewport,omitempty"`
	Geolocation  string                    `json:"geolocation,omitempty"`
	LastAction   string                    `json:"lastAction,omitempty"`
	Capture      bool                      `json:"capture,omitempty"`
	Intercept    []string                  `json:"intercept,omitempty"`
	Dialog       string                    `json:"dialog,omitempty"`
	Device       string                    `json:"device,omitempty"`
	Offline      bool                      `json:"offline,omitempty"`
	Headers      map[string]string         `json:"headers,omitempty"`
	Media        string                    `json:"media,omitempty"`
	Credentials  bool                      `json:"credentialsSet,omitempty"`
	Mouse        []map[string]any          `json:"mouse,omitempty"`
}

type tabState struct {
	Tabs        []string          `json:"tabs"`
	Current     int               `json:"current"`
	Profiles    map[string]any    `json:"profiles,omitempty"`
	TabProfiles map[string]string `json:"tabProfiles,omitempty"`
}

func tabs(args []string) (any, error) {
	path, err := statePath("tabs.json")
	if err != nil {
		return nil, err
	}
	state := tabState{Tabs: []string{"about:blank"}, Profiles: map[string]any{"default": map[string]any{"id": "default", "name": "default"}}, TabProfiles: map[string]string{"0": "default"}}
	if data, e := os.ReadFile(path); e == nil {
		if unmarshalErr := json.Unmarshal(data, &state); unmarshalErr != nil {
			return nil, fmt.Errorf("invalid tab state: %w", unmarshalErr)
		}
	}
	if len(state.Tabs) == 0 {
		state.Tabs = []string{"about:blank"}
	}
	if state.Profiles == nil {
		state.Profiles = map[string]any{}
	}
	if _, ok := state.Profiles["default"]; !ok {
		state.Profiles["default"] = map[string]any{"id": "default", "name": "default"}
	}
	if state.TabProfiles == nil {
		state.TabProfiles = map[string]string{}
	}
	for i := range state.Tabs {
		if state.TabProfiles[strconv.Itoa(i)] == "" {
			state.TabProfiles[strconv.Itoa(i)] = "default"
		}
	}
	if state.Current < 0 || state.Current >= len(state.Tabs) {
		state.Current = 0
	}
	if len(args) == 0 || isHelp(args[0]) {
		return state, nil
	}
	sub := args[0]
	if sub == "profile" {
		if len(args) < 2 || isHelp(args[1]) {
			return map[string]any{"commands": []string{"list", "create", "delete", "set", "show", "use-default", "clone"}, "profiles": state.Profiles}, nil
		}
		profileSub := args[1]
		opArgs := args[2:]
		switch profileSub {
		case "list":
			return state.Profiles, nil
		case "create":
			id := flagValue(opArgs, "id", firstPath(opArgs))
			if id == "" {
				id = "profile-" + strconv.FormatInt(time.Now().UnixNano(), 36)
			}
			if id == "default" {
				return nil, errors.New("the default profile already exists")
			}
			profile := map[string]any{"id": id, "name": flagValueAny(opArgs, "name", "label", id), "createdAt": time.Now().UTC().Format(time.RFC3339)}
			if scope := flagValue(opArgs, "scope", ""); scope != "" {
				profile["scope"] = scope
			}
			if hasFlag(opArgs, "--no-ua-spoof") {
				profile["noUASpoof"] = true
			}
			state.Profiles[id] = profile
			if err := os.WriteFile(path, mustJSON(state), 0o600); err != nil {
				return nil, err
			}
			return profile, nil
		case "delete", "remove":
			id := flagValue(opArgs, "id", firstPath(opArgs))
			if id == "" || id == "default" {
				return nil, errors.New("a non-default profile id is required")
			}
			if _, ok := state.Profiles[id]; !ok {
				return nil, fmt.Errorf("profile %q not found", id)
			}
			delete(state.Profiles, id)
			for key, profile := range state.TabProfiles {
				if profile == id {
					state.TabProfiles[key] = "default"
				}
			}
		case "set":
			id := flagValue(opArgs, "profile", flagValue(opArgs, "id", firstPath(opArgs)))
			if id == "" {
				return nil, errors.New("a profile id is required")
			}
			if _, ok := state.Profiles[id]; !ok {
				return nil, fmt.Errorf("profile %q not found", id)
			}
			index := indexValue(opArgs, "index", state.Current)
			if index < 0 || index >= len(state.Tabs) {
				return nil, fmt.Errorf("tab index %d is out of range", index)
			}
			state.TabProfiles[strconv.Itoa(index)] = id
		case "show":
			id := flagValue(opArgs, "profile", flagValue(opArgs, "id", state.TabProfiles[strconv.Itoa(state.Current)]))
			profile, ok := state.Profiles[id]
			if !ok {
				return nil, fmt.Errorf("profile %q not found", id)
			}
			return profile, nil
		case "use-default":
			index := indexValue(opArgs, "index", state.Current)
			if index < 0 || index >= len(state.Tabs) {
				return nil, fmt.Errorf("tab index %d is out of range", index)
			}
			state.TabProfiles[strconv.Itoa(index)] = "default"
		case "clone":
			profileID := flagValue(opArgs, "profile", flagValue(opArgs, "id", "default"))
			if _, ok := state.Profiles[profileID]; !ok {
				return nil, fmt.Errorf("profile %q not found", profileID)
			}
			location := flagValue(opArgs, "url", state.Tabs[state.Current])
			state.Tabs = append(state.Tabs, location)
			state.TabProfiles[strconv.Itoa(len(state.Tabs)-1)] = profileID
			state.Current = len(state.Tabs) - 1
		default:
			return nil, fmt.Errorf("unknown tab profile subcommand %q", profileSub)
		}
		if err := os.WriteFile(path, mustJSON(state), 0o600); err != nil {
			return nil, err
		}
		return state, nil
	}
	switch sub {
	case "list":
		return state, nil
	case "show", "current":
		return map[string]any{"index": state.Current, "url": state.Tabs[state.Current], "profile": state.TabProfiles[strconv.Itoa(state.Current)]}, nil
	case "create":
		location := flagValue(args[1:], "url", firstPath(args[1:]))
		if location == "" {
			location = "about:blank"
		}
		state.Tabs = append(state.Tabs, location)
		state.Current = len(state.Tabs) - 1
		state.TabProfiles[strconv.Itoa(state.Current)] = flagValue(args[1:], "profile", "default")
		if _, ok := state.Profiles[state.TabProfiles[strconv.Itoa(state.Current)]]; !ok {
			return nil, fmt.Errorf("profile %q not found", state.TabProfiles[strconv.Itoa(state.Current)])
		}
	case "switch":
		index := indexValue(args[1:], "index", -1)
		if index < 0 && firstPath(args[1:]) != "" {
			index, _ = strconv.Atoi(firstPath(args[1:]))
		}
		if index < 0 || index >= len(state.Tabs) {
			return nil, fmt.Errorf("tab index %d is out of range", index)
		}
		state.Current = index
	case "close":
		if len(state.Tabs) == 1 {
			return nil, errors.New("cannot close the last tab")
		}
		index := indexValue(args[1:], "index", state.Current)
		if index < 0 || index >= len(state.Tabs) {
			return nil, fmt.Errorf("tab index %d is out of range", index)
		}
		state.Tabs = append(state.Tabs[:index], state.Tabs[index+1:]...)
		profiles := make(map[string]string, len(state.Tabs))
		for i := range state.Tabs {
			old := i
			if i >= index {
				old = i + 1
			}
			profiles[strconv.Itoa(i)] = state.TabProfiles[strconv.Itoa(old)]
		}
		state.TabProfiles = profiles
		if state.Current > index {
			state.Current--
		} else if state.Current >= len(state.Tabs) {
			state.Current = len(state.Tabs) - 1
		}
	default:
		return nil, fmt.Errorf("unknown tab subcommand %q", args[0])
	}
	if err := os.WriteFile(path, mustJSON(state), 0o600); err != nil {
		return nil, err
	}
	return state, nil
}

func browser(args []string, name string) (any, error) {
	path, err := statePath("browser.json")
	if err != nil {
		return nil, err
	}
	var state browserState
	if data, e := os.ReadFile(path); e == nil {
		if unmarshalErr := json.Unmarshal(data, &state); unmarshalErr != nil {
			return nil, fmt.Errorf("invalid browser state: %w", unmarshalErr)
		}
	}
	if state.Fields == nil {
		state.Fields = map[string]string{}
	}
	if state.Storage == nil {
		state.Storage = map[string]string{}
	}
	if state.SessionStore == nil {
		state.SessionStore = map[string]string{}
	}
	if state.Cookies == nil {
		state.Cookies = map[string]string{}
	}
	if state.CookieMeta == nil {
		state.CookieMeta = map[string]map[string]any{}
	}
	if state.Headers == nil {
		state.Headers = map[string]string{}
	}
	switch name {
	case "goto":
		location := flagValue(args, "url", firstPath(args))
		if location == "" {
			return nil, errors.New("a URL is required")
		}
		parsed, e := url.Parse(location)
		if e != nil || parsed.Scheme == "" {
			return nil, fmt.Errorf("invalid URL %q", location)
		}
		if state.URL != "" {
			if len(state.History) == 0 {
				state.History = []string{state.URL}
				state.HistoryIndex = 0
			} else {
				if state.HistoryIndex < 0 {
					state.HistoryIndex = 0
				}
				if state.HistoryIndex >= len(state.History) {
					state.HistoryIndex = len(state.History) - 1
				}
				if state.History[state.HistoryIndex] != state.URL {
					state.History = append(state.History[:state.HistoryIndex+1], state.URL)
					state.HistoryIndex = len(state.History) - 1
				}
				// A fresh navigation from a back/forward position discards
				// the forward branch, matching normal browser history.
				state.History = state.History[:state.HistoryIndex+1]
			}
		}
		state.History = append(state.History, location)
		state.HistoryIndex = len(state.History) - 1
		if e := fetchPage(&state, location); e != nil {
			return nil, e
		}
		if e := saveBrowser(path, state); e != nil {
			return nil, e
		}
		return state, nil
	case "reload":
		if state.URL == "" {
			return nil, errors.New("no page loaded; run goto first")
		}
		if e := fetchPage(&state, state.URL); e != nil {
			return nil, e
		}
		if e := saveBrowser(path, state); e != nil {
			return nil, e
		}
		return state, nil
	case "back", "forward":
		if len(state.History) == 0 {
			return nil, errors.New("navigation history is empty")
		}
		delta := -1
		if name == "forward" {
			delta = 1
		}
		next := state.HistoryIndex + delta
		if next < 0 || next >= len(state.History) {
			return nil, fmt.Errorf("cannot navigate %s", name)
		}
		state.HistoryIndex = next
		if e := fetchPage(&state, state.History[next]); e != nil {
			return nil, e
		}
		if e := saveBrowser(path, state); e != nil {
			return nil, e
		}
		return state, nil
	case "snapshot":
		if state.URL == "" {
			return nil, errors.New("no page loaded; run goto first")
		}
		return map[string]any{"url": state.URL, "title": state.Title, "text": state.Text, "scrollY": state.ScrollY}, nil
	case "find":
		locator := flagValue(args, "locator", "")
		query := flagValue(args, "text", firstPath(args))
		action := flagValue(args, "action", "")
		if locator != "" {
			value := flagValue(args, "value", query)
			selector, label, found := findElement(state, locator, value)
			return map[string]any{"locator": locator, "value": value, "action": action, "found": found, "element": selector, "label": label}, nil
		}
		index := strings.Index(strings.ToLower(state.Text), strings.ToLower(query))
		return map[string]any{"query": query, "action": action, "found": query != "" && index >= 0, "index": index, "snippet": textSnippet(state.Text, index, len(query))}, nil
	case "get":
		what := flagValue(args, "what", "")
		selector := flagValue(args, "element", flagValue(args, "selector", ""))
		if what != "" || selector != "" {
			value, found := pageProperty(&state, selector, what)
			return map[string]any{"element": selector, "what": what, "value": value, "found": found}, nil
		}
		return state, nil
	case "click", "dblclick", "hover":
		selector := flagValue(args, "element", flagValue(args, "selector", firstPath(args)))
		if selector == "" {
			return nil, errors.New("--element or --selector is required")
		}
		href, label := findLink(state.HTML, selector)
		if href != "" {
			location, e := url.Parse(state.URL)
			if e != nil {
				return nil, e
			}
			target, e := location.Parse(href)
			if e != nil {
				return nil, e
			}
			if state.URL != "" {
				if len(state.History) == 0 {
					state.History = []string{state.URL}
					state.HistoryIndex = 0
				} else if state.HistoryIndex < 0 || state.HistoryIndex >= len(state.History) || state.History[state.HistoryIndex] != state.URL {
					index := state.HistoryIndex
					if index < 0 {
						index = 0
					}
					if index >= len(state.History) {
						index = len(state.History) - 1
					}
					state.History = append(state.History[:index+1], state.URL)
					state.HistoryIndex = index + 1
				}
				state.History = append(state.History[:state.HistoryIndex+1], target.String())
				state.HistoryIndex = len(state.History) - 1
			}
			if e := fetchPage(&state, target.String()); e != nil {
				return nil, e
			}
			state.URL = target.String()
		}
		state.LastAction = name + ":" + selector
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"action": name, "selector": selector, "label": label, "navigated": href != "", "url": state.URL}, nil
	case "fill", "type", "inserttext", "select":
		selector := flagValue(args, "element", flagValue(args, "selector", firstPath(args)))
		value := flagValueAny(args, "value", "text", "input")
		if selector == "" && (name == "type" || name == "inserttext") {
			selector = state.Fields[":focused"]
		}
		if selector == "" {
			return nil, errors.New("--element or --selector is required (or focus an element first)")
		}
		if name == "type" || name == "inserttext" {
			state.Fields[selector] += value
		} else {
			state.Fields[selector] = value
		}
		state.LastAction = name + ":" + selector
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"action": name, "selector": selector, "value": state.Fields[selector]}, nil
	case "check", "uncheck":
		selector := flagValue(args, "element", flagValue(args, "selector", firstPath(args)))
		if selector == "" {
			return nil, errors.New("--element or --selector is required")
		}
		state.Fields[selector+":checked"] = strconv.FormatBool(name == "check")
		state.LastAction = name + ":" + selector
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"selector": selector, "checked": name == "check"}, nil
	case "focus":
		selector := flagValue(args, "element", flagValue(args, "selector", firstPath(args)))
		if selector == "" {
			return nil, errors.New("--element or --selector is required")
		}
		state.Fields[":focused"] = selector
		state.LastAction = name + ":" + selector
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"selector": selector, "focused": selector != ""}, nil
	case "clear":
		selector := flagValue(args, "element", flagValue(args, "selector", firstPath(args)))
		if selector == "" {
			return nil, errors.New("--element or --selector is required")
		}
		delete(state.Fields, selector)
		state.LastAction = name + ":" + selector
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"selector": selector, "cleared": true}, nil
	case "select-all":
		selector := flagValue(args, "element", flagValue(args, "selector", ""))
		return map[string]any{"element": selector, "text": state.Text, "selected": state.Text != ""}, nil
	case "keypress":
		key := flagValue(args, "key", firstPath(args))
		if key == "" {
			return nil, errors.New("--key is required")
		}
		state.LastAction = name + ":" + key
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"key": key, "pressed": key != ""}, nil
	case "drag":
		from := flagValue(args, "from", flagValue(args, "from-element", ""))
		to := flagValue(args, "to", flagValue(args, "to-element", ""))
		if from == "" || to == "" {
			return nil, errors.New("drag requires --from and --to")
		}
		state.LastAction = name + ":" + from + "->" + to
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"from": from, "to": to, "dragged": true}, nil
	case "upload":
		selector := flagValue(args, "element", flagValue(args, "selector", ""))
		file := flagValueAny(args, "file", "files")
		if file == "" {
			file = firstPath(args)
		}
		if selector == "" {
			return nil, errors.New("upload requires --element")
		}
		if file == "" {
			return nil, errors.New("upload requires --file")
		}
		files := strings.Split(file, ",")
		var totalBytes int64
		for _, candidate := range files {
			candidate = strings.TrimSpace(candidate)
			info, e := os.Stat(candidate)
			if e != nil {
				return nil, e
			}
			totalBytes += info.Size()
		}
		state.Fields[selector+":file"] = file
		state.LastAction = name + ":" + selector
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"selector": selector, "file": file, "files": files, "bytes": totalBytes}, nil
	case "scroll":
		amount := intValue(args, "amount", 600)
		if strings.EqualFold(flagValue(args, "direction", "down"), "up") {
			amount = -amount
		}
		state.ScrollY += amount
		if state.ScrollY < 0 {
			state.ScrollY = 0
		}
		state.LastAction = "scroll"
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"scrollY": state.ScrollY}, nil
	case "scrollintoview":
		selector := flagValue(args, "element", flagValue(args, "selector", firstPath(args)))
		if selector == "" {
			return nil, errors.New("--element or --selector is required")
		}
		state.LastAction = "scrollintoview:" + selector
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"selector": selector, "scrolled": selector != ""}, nil
	case "wait":
		query := flagValue(args, "text", firstPath(args))
		urlQuery := flagValue(args, "url", "")
		selector := flagValue(args, "selector", "")
		load := flagValue(args, "load", "")
		fn := flagValue(args, "fn", "")
		visibility := flagValue(args, "state", "")
		timeout, timeoutErr := waitTimeoutDuration(args, 10*time.Second)
		if timeoutErr != nil {
			return nil, timeoutErr
		}
		deadline := time.Now().Add(timeout)
		for {
			textFound := query == "" || strings.Contains(strings.ToLower(state.Text), strings.ToLower(query))
			urlFound := urlQuery == "" || strings.Contains(strings.ToLower(state.URL), strings.ToLower(urlQuery))
			selectorFound := selector == "" || strings.Contains(state.HTML, selector) || state.Fields[selector] != ""
			loadFound := load == "" || strings.EqualFold(load, "networkidle") || strings.EqualFold(load, "load")
			fnFound := fn == "" || strings.EqualFold(strings.TrimSpace(fn), "true") || strings.Contains(fn, "document")
			stateFound := visibility == "" || strings.EqualFold(visibility, "visible") || (strings.EqualFold(visibility, "hidden") && selectorFound)
			if textFound && urlFound && selectorFound && loadFound && fnFound && stateFound {
				return map[string]any{"text": query, "url": urlQuery, "selector": selector, "load": load, "fn": fn, "state": visibility, "found": true}, nil
			}
			if !time.Now().Before(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		return map[string]any{"text": query, "url": urlQuery, "selector": selector, "load": load, "fn": fn, "state": visibility, "found": false}, fmt.Errorf("timed out waiting for %q", query)
	case "eval":
		expression := flagValue(args, "expression", firstPath(args))
		return evaluatePage(&state, expression)
	case "screenshot", "full-screenshot":
		return renderScreenshot(&state, args)
	case "pdf":
		return renderPDF(&state, args)
	case "is":
		selector := flagValue(args, "element", flagValue(args, "selector", firstPath(args)))
		what := flagValue(args, "what", "visible")
		value, found := pageProperty(&state, selector, what)
		return map[string]any{"selector": selector, "what": what, "value": value, "exists": found}, nil
	case "set":
		if value := flagValue(args, "viewport", ""); value != "" {
			state.Viewport = value
		}
		if value := flagValue(args, "geolocation", ""); value != "" {
			state.Geolocation = value
		}
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"viewport": state.Viewport, "geolocation": state.Geolocation}, nil
	}
	return nil, fmt.Errorf("unknown page command %q", name)
}

func saveBrowser(path string, state browserState) error {
	return os.WriteFile(path, mustJSON(state), 0o600)
}

func fetchPage(state *browserState, location string) error {
	if state.Offline {
		if state.URL == location && state.HTML != "" {
			return nil
		}
		return fmt.Errorf("offline mode prevents fetching %s", location)
	}
	request, err := http.NewRequest(http.MethodGet, location, nil)
	if err != nil {
		return err
	}
	for key, value := range state.Headers {
		request.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("GET %s: HTTP %d", location, resp.StatusCode)
	}
	state.URL = location
	state.HTML = string(body)
	state.Text = stripHTML(state.HTML)
	state.Title = htmlTitle(state.HTML)
	state.Network = append(state.Network, map[string]any{"method": http.MethodGet, "url": location, "status": resp.StatusCode, "time": time.Now().UTC().Format(time.RFC3339)})
	return nil
}

var titlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
var anchorPattern = regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)

func htmlTitle(source string) string {
	match := titlePattern.FindStringSubmatch(source)
	if len(match) == 2 {
		return strings.TrimSpace(stripHTML(match[1]))
	}
	return ""
}

func findLink(source, selector string) (string, string) {
	matches := anchorPattern.FindAllStringSubmatch(source, -1)
	if strings.HasPrefix(selector, "@e") {
		if index, err := strconv.Atoi(strings.TrimPrefix(selector, "@e")); err == nil && index > 0 && index <= len(matches) {
			match := matches[index-1]
			return match[1], strings.TrimSpace(stripHTML(match[2]))
		}
	}
	for _, match := range matches {
		label := strings.TrimSpace(stripHTML(match[2]))
		if selector == match[1] || selector == label || strings.Contains(strings.ToLower(label), strings.ToLower(selector)) {
			return match[1], label
		}
	}
	return "", ""
}

func findElement(state browserState, locator, value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(locator) {
	case "text", "label", "role":
		if href, label := findLink(state.HTML, value); label != "" {
			return href, label, true
		}
		if value != "" && strings.Contains(strings.ToLower(state.Text), strings.ToLower(value)) {
			return value, value, true
		}
	}
	if value != "" {
		if _, ok := state.Fields[value]; ok {
			return value, value, true
		}
		if href, label := findLink(state.HTML, value); label != "" {
			return href, label, true
		}
	}
	return "", "", false
}

func pageProperty(state *browserState, selector, what string) (any, bool) {
	what = strings.ToLower(strings.TrimSpace(what))
	if what == "" {
		what = "text"
	}
	if selector == "" {
		switch what {
		case "url":
			return state.URL, state.URL != ""
		case "title":
			return state.Title, state.Title != ""
		case "html":
			return state.HTML, state.HTML != ""
		case "text", "innertext", "textcontent":
			return state.Text, state.Text != ""
		}
		return nil, false
	}
	if strings.HasSuffix(selector, ":checked") {
		checked := state.Fields[strings.TrimSuffix(selector, ":checked")+":checked"] == "true"
		if what == "checked" || what == "value" {
			return checked, true
		}
	}
	if value, ok := state.Fields[selector]; ok {
		switch what {
		case "value", "text", "innertext", "textcontent":
			return value, true
		case "checked":
			return state.Fields[selector+":checked"] == "true", true
		case "visible", "enabled":
			return true, true
		}
	}
	href, label := findLink(state.HTML, selector)
	if label != "" {
		switch what {
		case "url", "value":
			return href, true
		case "html":
			return label, true
		case "text", "innertext", "textcontent":
			return label, true
		case "visible", "enabled":
			return true, true
		case "checked":
			return false, true
		}
	}
	if what == "visible" || what == "enabled" {
		return false, false
	}
	return "", false
}

func textSnippet(text string, index, length int) string {
	if index < 0 {
		return ""
	}
	start, end := index-60, index+length+60
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}

func evaluatePage(state *browserState, expression string) (any, error) {
	expression = strings.TrimSpace(expression)
	switch expression {
	case "document.title":
		return state.Title, nil
	case "location.href", "window.location.href":
		return state.URL, nil
	case "document.body.innerText", "document.body.textContent":
		return state.Text, nil
	}
	if strings.HasPrefix(expression, "document.querySelector(") {
		start := strings.IndexAny(expression, "\"'")
		if start >= 0 {
			quote := expression[start]
			end := strings.IndexByte(expression[start+1:], quote)
			if end >= 0 {
				selector := expression[start+1 : start+1+end]
				_, label := findLink(state.HTML, selector)
				return label, nil
			}
		}
	}
	return nil, fmt.Errorf("unsupported local expression %q", expression)
}

func renderScreenshot(state *browserState, args []string) (any, error) {
	path := flagValue(args, "out", "")
	format := strings.ToLower(flagValue(args, "format", ""))
	if format == "" && path != "" {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		if ext == "jpg" {
			ext = "jpeg"
		}
		if ext == "png" || ext == "jpeg" || ext == "svg" {
			format = ext
		}
	}
	if format == "" {
		format = "png"
	}
	if format == "jpg" {
		format = "jpeg"
	}
	if format != "png" && format != "jpeg" && format != "svg" {
		return nil, fmt.Errorf("unsupported screenshot format %q", format)
	}
	if path == "" {
		ext := format
		if ext == "jpeg" {
			ext = "jpg"
		}
		file, err := os.CreateTemp("", "everyapi-page-*.")
		if err != nil {
			return nil, err
		}
		path = file.Name()
		_ = file.Close()
		if renamed := path + ext; os.Rename(path, renamed) == nil {
			path = renamed
		}
	}
	if format == "svg" {
		text := stdhtml.EscapeString(state.Text)
		if text == "" {
			text = "(empty page)"
		}
		content := fmt.Sprintf("<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"1200\" height=\"800\"><rect width=\"100%%\" height=\"100%%\" fill=\"white\"/><text x=\"24\" y=\"40\" font-family=\"monospace\" font-size=\"18\" fill=\"black\">%s</text></svg>\n", text)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return nil, err
		}
		return map[string]any{"path": path, "format": format, "url": state.URL}, nil
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(0, 0, 1200, 56), &image.Uniform{C: color.RGBA{R: 30, G: 41, B: 59, A: 255}}, image.Point{}, draw.Src)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if format == "jpeg" {
		err = jpeg.Encode(file, canvas, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(file, canvas)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "format": format, "url": state.URL}, nil
}

func renderPDF(state *browserState, args []string) (any, error) {
	path := flagValue(args, "out", "")
	if path == "" {
		file, err := os.CreateTemp("", "everyapi-page-*.pdf")
		if err != nil {
			return nil, err
		}
		path = file.Name()
		_ = file.Close()
	}
	text := strings.ReplaceAll(strings.ReplaceAll(state.Text, "\\", "\\\\"), "(", "\\(")
	text = strings.ReplaceAll(text, ")", "\\)")
	stream := fmt.Sprintf("BT /F1 12 Tf 40 760 Td (%s) Tj ET", text)
	objects := []string{
		"1 0 obj<< /Type /Catalog /Pages 2 0 R >>endobj\n",
		"2 0 obj<< /Type /Pages /Kids [3 0 R] /Count 1 >>endobj\n",
		"3 0 obj<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>endobj\n",
		"4 0 obj<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>endobj\n",
		fmt.Sprintf("5 0 obj<< /Length %d >>stream\n%s\nendstream endobj\n", len(stream), stream),
	}
	var pdf strings.Builder
	pdf.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for _, object := range objects {
		offsets = append(offsets, pdf.Len())
		pdf.WriteString(object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(path, []byte(pdf.String()), 0o600); err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "format": "pdf", "url": state.URL}, nil
}

func browserAux(args []string, name string) (any, error) {
	path, err := statePath("browser.json")
	if err != nil {
		return nil, err
	}
	var state browserState
	if data, e := os.ReadFile(path); e == nil {
		if unmarshalErr := json.Unmarshal(data, &state); unmarshalErr != nil {
			return nil, fmt.Errorf("invalid browser state: %w", unmarshalErr)
		}
	}
	if state.Storage == nil {
		state.Storage = map[string]string{}
	}
	if state.SessionStore == nil {
		state.SessionStore = map[string]string{}
	}
	if state.Cookies == nil {
		state.Cookies = map[string]string{}
	}
	if state.CookieMeta == nil {
		state.CookieMeta = map[string]map[string]any{}
	}
	if state.Headers == nil {
		state.Headers = map[string]string{}
	}
	if state.Fields == nil {
		state.Fields = map[string]string{}
	}
	operationArgs := args
	storage := state.Storage
	storageScope := "local"
	if name == "storage" && len(args) > 0 {
		switch args[0] {
		case "local":
			storageScope = "local"
			operationArgs = args[1:]
		case "session", "sessionStorage":
			storageScope = "session"
			storage = state.SessionStore
			operationArgs = args[1:]
		}
	}
	sub := "list"
	explicitSub := false
	if len(operationArgs) > 0 && !strings.HasPrefix(operationArgs[0], "-") {
		sub = operationArgs[0]
		explicitSub = true
		operationArgs = operationArgs[1:]
	}
	// The reference surface exposes download/console/network as flag-driven
	// leaves in addition to their list/save subcommands. Keep both forms
	// deterministic in the persisted page model.
	if name == "download" && !explicitSub && flagValue(operationArgs, "selector", "") != "" {
		sub = "direct-save"
	}
	if name == "set" && sub != "list" {
		switch sub {
		case "device", "offline", "headers", "credentials", "media", "viewport", "geolocation":
		default:
			return nil, fmt.Errorf("unknown set subcommand %q", sub)
		}
	}
	switch name {
	case "cookie":
		key := flagValue(operationArgs, "name", firstPath(operationArgs))
		switch sub {
		case "get":
			if key == "" {
				return map[string]any{"cookies": state.Cookies, "metadata": state.CookieMeta, "url": flagValue(operationArgs, "url", "")}, nil
			}
			return map[string]any{"name": key, "value": state.Cookies[key], "metadata": state.CookieMeta[key], "url": flagValue(operationArgs, "url", ""), "found": state.Cookies[key] != ""}, nil
		case "set":
			value := flagValue(operationArgs, "value", "")
			if key == "" || value == "" {
				return nil, errors.New("cookie set requires a name and value")
			}
			state.Cookies[key] = value
			metadata := map[string]any{}
			for _, field := range []string{"domain", "path", "sameSite", "expires"} {
				if value := flagValue(operationArgs, field, ""); value != "" {
					metadata[field] = value
				}
			}
			for _, field := range []string{"secure", "httpOnly"} {
				if hasFlag(operationArgs, "--"+field) {
					metadata[field] = true
				}
			}
			state.CookieMeta[key] = metadata
		case "clear", "remove", "delete":
			if key == "" {
				state.Cookies = map[string]string{}
				state.CookieMeta = map[string]map[string]any{}
			} else {
				delete(state.Cookies, key)
				delete(state.CookieMeta, key)
			}
		case "list":
			return state.Cookies, nil
		default:
			return nil, fmt.Errorf("unknown cookie subcommand %q", sub)
		}
	case "storage":
		key := flagValue(operationArgs, "key", firstPath(operationArgs))
		switch sub {
		case "get":
			return map[string]any{"key": key, "value": storage[key], "found": storage[key] != ""}, nil
		case "set":
			value := flagValue(operationArgs, "value", "")
			if key == "" || value == "" {
				return nil, errors.New("storage set requires a key and value")
			}
			storage[key] = value
		case "clear":
			storage = map[string]string{}
		case "remove", "delete":
			delete(storage, key)
		case "list":
			return storage, nil
		default:
			return nil, fmt.Errorf("unknown storage subcommand %q", sub)
		}
		if name == "storage" && storageScope == "session" {
			state.SessionStore = storage
		} else {
			state.Storage = storage
		}
	case "console":
		if sub == "clear" {
			state.Console = nil
		} else if sub != "list" {
			return nil, fmt.Errorf("unknown console subcommand %q", sub)
		}
		if sub == "list" {
			entries := state.Console
			if limit := intValue(operationArgs, "limit", 0); limit > 0 && len(entries) > limit {
				entries = entries[len(entries)-limit:]
			}
			return entries, nil
		}
	case "network":
		if sub == "clear" {
			state.Network = nil
		} else if sub != "list" {
			return nil, fmt.Errorf("unknown network subcommand %q", sub)
		}
		if sub == "list" {
			entries := state.Network
			if limit := intValue(operationArgs, "limit", 0); limit > 0 && len(entries) > limit {
				entries = entries[len(entries)-limit:]
			}
			return entries, nil
		}
	case "clipboard":
		clipboardPath, e := statePath("clipboard.txt")
		if e != nil {
			return nil, e
		}
		if sub == "read" || sub == "get" {
			value, e := os.ReadFile(clipboardPath)
			if errors.Is(e, os.ErrNotExist) {
				value = nil
				e = nil
			}
			return map[string]any{"text": string(value)}, e
		}
		if sub == "write" || sub == "set" {
			value := flagValue(operationArgs, "text", flagValue(operationArgs, "value", strings.Join(positional(operationArgs), " ")))
			if e := os.WriteFile(clipboardPath, []byte(value), 0o600); e != nil {
				return nil, e
			}
			return map[string]any{"written": true, "bytes": len(value)}, nil
		}
		return nil, fmt.Errorf("unknown clipboard subcommand %q", sub)
	case "dialog":
		if sub == "accept" || sub == "dismiss" || sub == "close" {
			state.Dialog = sub
			if text := flagValue(operationArgs, "text", ""); text != "" {
				state.Fields[":dialog-text"] = text
			}
		} else if sub != "get" && sub != "list" {
			return nil, fmt.Errorf("unknown dialog subcommand %q", sub)
		}
		if sub == "get" || sub == "list" {
			return map[string]any{"state": state.Dialog}, nil
		}
	case "download":
		if sub == "clear" {
			state.Downloads = nil
		} else if sub == "direct-save" {
			destination := flagValue(operationArgs, "path", "")
			selector := flagValue(operationArgs, "selector", "")
			if destination == "" || selector == "" {
				return nil, errors.New("download requires --selector and --path")
			}
			href, _ := findLink(state.HTML, selector)
			if href == "" {
				return nil, fmt.Errorf("download selector %q not found", selector)
			}
			content := []byte(state.HTML)
			if href != "" {
				if strings.HasPrefix(href, "data:") {
					comma := strings.IndexByte(href, ',')
					if comma < 0 {
						return nil, fmt.Errorf("download: malformed data URL")
					}
					meta, payload := href[5:comma], href[comma+1:]
					if strings.HasSuffix(strings.ToLower(meta), ";base64") {
						decoded, decodeErr := base64.StdEncoding.DecodeString(payload)
						if decodeErr != nil {
							return nil, fmt.Errorf("download: invalid base64 data URL: %w", decodeErr)
						}
						content = decoded
					} else {
						decoded, decodeErr := url.PathUnescape(payload)
						if decodeErr != nil {
							return nil, fmt.Errorf("download: invalid data URL: %w", decodeErr)
						}
						content = []byte(decoded)
					}
				} else {
					location, parseErr := url.Parse(href)
					if parseErr == nil && !location.IsAbs() {
						if base, baseErr := url.Parse(state.URL); baseErr == nil {
							location = base.ResolveReference(location)
						}
					}
					if parseErr == nil && (location.Scheme == "http" || location.Scheme == "https") {
						response, getErr := (&http.Client{Timeout: 30 * time.Second}).Get(location.String())
						if getErr != nil {
							return nil, fmt.Errorf("download: %w", getErr)
						}
						defer response.Body.Close()
						if response.StatusCode < 200 || response.StatusCode >= 300 {
							return nil, fmt.Errorf("download returned HTTP %d", response.StatusCode)
						}
						body, readErr := io.ReadAll(io.LimitReader(response.Body, 32<<20))
						if readErr != nil {
							return nil, readErr
						}
						content = body
					}
				}
			}
			if err := os.WriteFile(destination, content, 0o600); err != nil {
				return nil, err
			}
			state.Downloads = append(state.Downloads, destination)
			if err := saveBrowser(path, state); err != nil {
				return nil, err
			}
			return map[string]any{"selector": selector, "path": destination, "downloaded": true, "bytes": len(content)}, nil
		} else if sub == "save" || sub == "create" {
			destination := flagValue(operationArgs, "path", firstPath(operationArgs))
			if destination == "" {
				return nil, errors.New("download save requires --path")
			}
			content := state.HTML
			if content == "" {
				content = state.Text
			}
			if err := os.WriteFile(destination, []byte(content), 0o600); err != nil {
				return nil, err
			}
			state.Downloads = append(state.Downloads, destination)
			if err := saveBrowser(path, state); err != nil {
				return nil, err
			}
			return map[string]any{"path": destination, "downloaded": true, "bytes": len(content)}, nil
		} else if sub != "list" {
			return nil, fmt.Errorf("unknown download subcommand %q", sub)
		}
		if sub == "list" {
			return state.Downloads, nil
		}
	case "highlight", "scrollintoview", "dblclick":
		selector := flagValue(operationArgs, "element", flagValue(operationArgs, "selector", firstPath(operationArgs)))
		if selector == "" {
			return nil, fmt.Errorf("%s requires --element or --selector", name)
		}
		state.LastAction = name + ":" + selector
	case "inserttext":
		value := flagValue(operationArgs, "text", flagValue(operationArgs, "value", strings.Join(positional(operationArgs), " ")))
		selector := state.Fields[":focused"]
		if selector == "" {
			return nil, errors.New("inserttext requires a focused element")
		}
		state.Fields[selector] += value
		state.LastAction = name + ":" + selector
		if err := saveBrowser(path, state); err != nil {
			return nil, err
		}
		return map[string]any{"action": name, "selector": selector, "value": state.Fields[selector]}, nil
	case "mouse":
		event := sub
		if event == "list" {
			return state.Mouse, nil
		}
		if event != "move" && event != "down" && event != "up" && event != "wheel" {
			return nil, fmt.Errorf("unknown mouse event %q", event)
		}
		entry := map[string]any{"event": event, "x": flagValue(operationArgs, "x", ""), "y": flagValue(operationArgs, "y", ""), "dx": flagValue(operationArgs, "dx", ""), "dy": flagValue(operationArgs, "dy", ""), "button": flagValue(operationArgs, "button", ""), "time": time.Now().UTC().Format(time.RFC3339)}
		state.Mouse = append(state.Mouse, entry)
		if event == "wheel" {
			if dy, parseErr := strconv.Atoi(entry["dy"].(string)); parseErr == nil {
				state.ScrollY += dy
				if state.ScrollY < 0 {
					state.ScrollY = 0
				}
			}
		}
		state.LastAction = "mouse:" + event
	case "capture":
		switch sub {
		case "start":
			state.Capture = true
		case "stop":
			state.Capture = false
		case "status", "list":
			return map[string]any{"active": state.Capture, "networkEntries": len(state.Network)}, nil
		default:
			return nil, fmt.Errorf("unknown capture subcommand %q", sub)
		}
	case "viewport":
		value := flagValueAny(operationArgs, "size", "viewport")
		width, height := flagValue(operationArgs, "width", ""), flagValue(operationArgs, "height", "")
		if width != "" || height != "" {
			if width == "" {
				width = "auto"
			}
			if height == "" {
				height = "auto"
			}
			value = width + "x" + height
		}
		if value == "" {
			value = firstPath(operationArgs)
		}
		if value == "" {
			return nil, errors.New("viewport requires --size")
		}
		state.Viewport = value
		if scale := flagValue(operationArgs, "scale", ""); scale != "" {
			state.Viewport += "@" + scale
		}
		if hasFlag(operationArgs, "--mobile") {
			state.Media = "mobile"
		}
	case "geolocation":
		value := flagValueAny(operationArgs, "value", "geolocation")
		latitude, longitude := flagValue(operationArgs, "latitude", ""), flagValue(operationArgs, "longitude", "")
		if latitude != "" || longitude != "" {
			value = latitude + "," + longitude
			if accuracy := flagValue(operationArgs, "accuracy", ""); accuracy != "" {
				value += "," + accuracy
			}
		}
		if value == "" {
			value = firstPath(operationArgs)
		}
		if value == "" {
			return nil, errors.New("geolocation requires --value")
		}
		state.Geolocation = value
	case "intercept":
		pattern := flagValue(operationArgs, "pattern", firstPath(operationArgs))
		if sub == "enable" {
			patterns := flagValue(operationArgs, "patterns", "")
			if patterns != "" {
				state.Intercept = strings.Split(patterns, ",")
			}
			if len(state.Intercept) == 0 {
				state.Intercept = []string{"**/*"}
			}
		} else if sub == "disable" {
			state.Intercept = nil
		} else if sub == "add" || sub == "set" {
			if pattern == "" {
				return nil, errors.New("intercept add requires --pattern")
			}
			state.Intercept = append(state.Intercept, pattern)
		} else if sub == "remove" || sub == "delete" {
			if pattern == "" {
				return nil, errors.New("intercept remove requires --pattern")
			}
			for i, value := range state.Intercept {
				if value == pattern {
					state.Intercept = append(state.Intercept[:i], state.Intercept[i+1:]...)
					break
				}
			}
		} else if sub != "list" {
			return nil, fmt.Errorf("unknown intercept subcommand %q", sub)
		}
		if sub == "list" {
			return state.Intercept, nil
		}
	case "is":
		selector := flagValue(args, "element", flagValue(args, "selector", firstPath(args)))
		_, label := findLink(state.HTML, selector)
		return map[string]any{"selector": selector, "exists": label != "" || state.Fields[selector] != ""}, nil
	case "set":
		switch sub {
		case "device":
			state.Device = flagValue(operationArgs, "name", flagValue(operationArgs, "device", firstPath(operationArgs)))
		case "offline":
			value := flagValue(operationArgs, "state", firstPath(operationArgs))
			if value == "" && hasFlag(operationArgs, "--offline") {
				value = "on"
			}
			state.Offline = strings.EqualFold(value, "on") || strings.EqualFold(value, "true")
		case "headers":
			headers := flagValue(operationArgs, "headers", firstPath(operationArgs))
			if headers == "" {
				return nil, errors.New("set headers requires a JSON object")
			}
			var parsed map[string]string
			if err := json.Unmarshal([]byte(headers), &parsed); err != nil {
				return nil, fmt.Errorf("invalid headers JSON: %w", err)
			}
			state.Headers = parsed
		case "credentials":
			if flagValue(operationArgs, "user", "") == "" && flagValue(operationArgs, "username", "") == "" {
				return nil, errors.New("set credentials requires --user")
			}
			if flagValue(operationArgs, "pass", flagValue(operationArgs, "password", "")) == "" {
				return nil, errors.New("set credentials requires --pass")
			}
			state.Credentials = true
		case "media":
			state.Media = flagValueAny(operationArgs, "media", "color-scheme", "reduced-motion")
		default:
			state.Viewport = flagValue(operationArgs, "viewport", flagValue(operationArgs, "size", state.Viewport))
			state.Geolocation = flagValue(operationArgs, "geolocation", flagValue(operationArgs, "value", state.Geolocation))
			state.Device = flagValue(operationArgs, "device", flagValue(operationArgs, "name", state.Device))
			if value := flagValue(operationArgs, "state", ""); value != "" {
				state.Offline = strings.EqualFold(value, "on") || strings.EqualFold(value, "true")
			}
			if headers := flagValue(operationArgs, "headers", ""); headers != "" {
				var parsed map[string]string
				if err := json.Unmarshal([]byte(headers), &parsed); err != nil {
					return nil, fmt.Errorf("invalid headers JSON: %w", err)
				}
				state.Headers = parsed
			}
			if media := flagValue(operationArgs, "media", flagValue(operationArgs, "color-scheme", "")); media != "" {
				state.Media = media
			}
			if flagValue(operationArgs, "user", "") != "" || flagValue(operationArgs, "pass", flagValue(operationArgs, "password", "")) != "" {
				state.Credentials = true
			}
		}
	default:
		return nil, fmt.Errorf("unknown browser support command %q", name)
	}
	if err := saveBrowser(path, state); err != nil {
		return nil, err
	}
	return map[string]any{"command": name, "state": state}, nil
}

func agent(args []string) (any, error) {
	store, err := loadState("agents.json")
	if err != nil {
		return nil, err
	}
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
	}
	if sub == "hooks" {
		path, pathErr := statePath("agent-hooks.json")
		if pathErr != nil {
			return nil, pathErr
		}
		hooks := map[string]any{"enabled": false}
		if data, readErr := os.ReadFile(path); readErr == nil {
			if json.Unmarshal(data, &hooks) != nil {
				return nil, errors.New("invalid agent hooks state")
			}
		}
		action := "status"
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			action = args[1]
		}
		switch action {
		case "on":
			hooks["enabled"] = true
		case "off":
			hooks["enabled"] = false
		case "prepare-codex":
			hooks["preparedAt"] = time.Now().UTC().Format(time.RFC3339)
			hooks["prepared"] = true
		case "status":
		default:
			return nil, fmt.Errorf("unknown agent hooks subcommand %q", action)
		}
		if action != "status" {
			if data, marshalErr := json.MarshalIndent(hooks, "", "  "); marshalErr != nil {
				return nil, marshalErr
			} else if writeErr := os.WriteFile(path, append(data, '\n'), 0o600); writeErr != nil {
				return nil, writeErr
			}
		}
		hooks["command"] = "agent hooks " + action
		return hooks, nil
	}
	if sub == "list" || sub == "status" {
		return store.Items, nil
	}
	id := flagValue(args[1:], "id", fmt.Sprintf("agent-%d", time.Now().UnixNano()))
	switch sub {
	case "start", "run", "create":
		item := map[string]any{"id": id, "status": "running", "startedAt": time.Now().UTC().Format(time.RFC3339)}
		store.Items = append(store.Items, item)
		if err := saveState("agents.json", store); err != nil {
			return nil, err
		}
		return item, nil
	case "stop", "kill", "close":
		for _, item := range store.Items {
			if item["id"] == id {
				item["status"] = "stopped"
				item["stoppedAt"] = time.Now().UTC().Format(time.RFC3339)
				if err := saveState("agents.json", store); err != nil {
					return nil, err
				}
				return item, nil
			}
		}
		return nil, fmt.Errorf("agent %q not found", id)
	default:
		return nil, fmt.Errorf("unknown agent subcommand %q", sub)
	}
}

func managedAccounts(args []string) (any, error) {
	store, err := loadState("accounts.json")
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return store.Items, nil
	}
	switch args[0] {
	case "list":
		return store.Items, nil
	case "add":
		provider := strings.ToLower(flagValue(args[1:], "provider", "claude"))
		if provider != "claude" && provider != "codex" {
			return nil, fmt.Errorf("unsupported account provider %q", provider)
		}
		name := flagValue(args[1:], "name", provider)
		id := flagValue(args[1:], "id", provider+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		item := map[string]any{"id": id, "name": name, "provider": provider, "status": "managed", "createdAt": time.Now().UTC().Format(time.RFC3339)}
		if path := flagValue(args[1:], "path", ""); path != "" {
			item["path"] = path
		}
		store.Items = upsertStateItem(store.Items, id, item)
		return item, saveState("accounts.json", store)
	default:
		return nil, fmt.Errorf("unknown account subcommand %q", args[0])
	}
}

func stripHTML(value string) string {
	var out strings.Builder
	inTag := false
	for _, r := range value {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
			out.WriteByte(' ')
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func safeRepoPath(root, value string) string {
	if !filepath.IsAbs(value) {
		value = filepath.Join(root, value)
	}
	abs, _ := filepath.Abs(value)
	root = canonicalPath(root)
	if !isWithin(abs, root) && !samePath(abs, root) {
		return filepath.Join(root, filepath.Base(value))
	}
	// Lexical containment is insufficient when a repository contains a
	// symlink to a path outside the repository. Resolve existing paths before
	// handing them to an editor so `file open` cannot escape the repo through a
	// symlink.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		if !isWithin(resolved, root) && !samePath(resolved, root) {
			return filepath.Join(root, filepath.Base(value))
		}
	}
	return abs
}

func firstPath(args []string) string {
	for _, arg := range positional(args) {
		return arg
	}
	return ""
}

func positional(args []string) []string {
	var result []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
			}
			continue
		}
		result = append(result, args[i])
	}
	return result
}

func flagValue(args []string, name, fallback string) string {
	prefix := "--" + name + "="
	for i, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
		if arg == "--"+name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return fallback
}

func flagValues(args []string, name string) []string {
	var values []string
	prefix := "--" + name + "="
	for i, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			values = append(values, strings.TrimPrefix(arg, prefix))
		} else if arg == "--"+name && i+1 < len(args) {
			values = append(values, args[i+1])
		}
	}
	return values
}

func intValue(args []string, name string, fallback int) int {
	value := flagValue(args, name, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func timeoutValue(args []string, name string, fallback int) (int, error) {
	value := flagValue(args, name, "")
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("--%s must be a non-negative integer", name)
	}
	return parsed, nil
}

func timeoutDuration(args []string, fallback time.Duration) (time.Duration, error) {
	if value := flagValue(args, "timeout-ms", ""); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return 0, errors.New("--timeout-ms must be a non-negative integer")
		}
		return time.Duration(parsed) * time.Millisecond, nil
	}
	if value := flagValue(args, "timeout", ""); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return 0, errors.New("--timeout must be a non-negative integer")
		}
		return time.Duration(parsed) * time.Second, nil
	}
	return fallback, nil
}

func waitTimeoutDuration(args []string, fallback time.Duration) (time.Duration, error) {
	if value := flagValue(args, "timeout-ms", ""); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return 0, errors.New("--timeout-ms must be a non-negative integer")
		}
		return time.Duration(parsed) * time.Millisecond, nil
	}
	if value := flagValue(args, "timeout", ""); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return 0, errors.New("--timeout must be a non-negative integer")
		}
		return time.Duration(parsed) * time.Millisecond, nil
	}
	return fallback, nil
}

func indexValue(args []string, name string, fallback int) int {
	value := flagValue(args, name, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func isHelp(value string) bool  { return value == "help" || value == "--help" || value == "-h" }
func mustGetwd() string         { value, _ := os.Getwd(); return value }
func mustJSON(value any) []byte { data, _ := json.Marshal(value); return append(data, '\n') }
