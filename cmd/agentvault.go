package cmd

// AgentSessions exposes a bounded, read-only projection of local third-party
// agent session files. The desktop app consumes this command as JSON instead
// of maintaining a second transcript scanner in its own native layer.

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
)

const (
	defaultAgentSessionLimit = 250
	maxAgentSessionResults   = 1000
	maxAgentSessionFiles     = 5000
	maxAgentSessionDepth     = 12
	maxAgentPrefixBytes      = 256 * 1024
	maxAgentJSONLines        = 512
)

const agentSessionsUsage = "usage: everyapi sessions list [--format=json] [--limit N]"

type AgentSession struct {
	Provider     string  `json:"provider"`
	SessionID    string  `json:"session_id"`
	Title        string  `json:"title"`
	CWD          *string `json:"cwd"`
	FirstPrompt  *string `json:"first_prompt"`
	FilePath     string  `json:"file_path"`
	ModifiedAt   float64 `json:"modified_at"`
	SizeBytes    float64 `json:"size_bytes"`
	MessageCount uint32  `json:"message_count"`
}

type AgentSessionScan struct {
	Sessions     []AgentSession `json:"sessions"`
	ScannedFiles uint32         `json:"scanned_files"`
	Truncated    bool           `json:"truncated"`
	Providers    []string       `json:"providers"`
}

type agentProviderRoot struct {
	provider string
	path     string
}

type agentCandidate struct {
	provider   string
	path       string
	modifiedAt float64
	sizeBytes  float64
}

// AgentSessions dispatches `everyapi sessions`. It is intentionally local and
// login-free: no transcript bytes leave this machine and no network call is
// needed to recover a session the user already owns.
func AgentSessions(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(agentSessionsUsage)
		return nil
	}
	if args[0] != "list" {
		return fmt.Errorf("unknown 'sessions' subcommand %q — try `everyapi sessions help`", args[0])
	}
	fs := flag.NewFlagSet("everyapi sessions list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "text", "output format (json or text)")
	limit := fs.Int("limit", defaultAgentSessionLimit, "maximum sessions to return")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	if *format != "json" && *format != "text" {
		return fmt.Errorf("unsupported sessions format %q — use json or text", *format)
	}
	if *limit < 1 {
		return errors.New("sessions limit must be at least 1")
	}
	if *limit > maxAgentSessionResults {
		*limit = maxAgentSessionResults
	}
	scan, err := scanAgentSessions(*limit)
	if err != nil {
		return err
	}
	if *format == "json" {
		encoded, err := json.Marshal(scan)
		if err != nil {
			return err
		}
		cliout.Println(string(encoded))
		return nil
	}
	for _, session := range scan.Sessions {
		cliout.Printf("%-8s  %-36s  %s\n", session.Provider, session.SessionID, session.Title)
	}
	if len(scan.Sessions) == 0 {
		cliout.Println("No local agent sessions found.")
	}
	return nil
}

func scanAgentSessions(limit int) (AgentSessionScan, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return AgentSessionScan{}, err
	}
	return scanAgentSessionsAt(agentProviderRoots(home), limit)
}

func scanAgentSessionsAt(roots []agentProviderRoot, limit int) (AgentSessionScan, error) {
	candidates := make([]agentCandidate, 0, 128)
	providers := make([]string, 0, len(roots))
	truncated := false
	for _, root := range roots {
		info, err := os.Stat(root.path)
		if err != nil || !info.IsDir() {
			continue
		}
		providers = append(providers, root.provider)
		if err := discoverAgentFiles(root, &candidates, &truncated); err != nil {
			return AgentSessionScan{}, err
		}
		if len(candidates) >= maxAgentSessionFiles {
			truncated = true
			break
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].modifiedAt != candidates[j].modifiedAt {
			return candidates[i].modifiedAt > candidates[j].modifiedAt
		}
		return candidates[i].path < candidates[j].path
	})
	scanned := len(candidates)
	if scanned > int(^uint32(0)) {
		scanned = int(^uint32(0))
	}
	truncated = truncated || len(candidates) > limit
	sessions := make([]AgentSession, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		if len(sessions) >= limit {
			break
		}
		if session, ok := projectAgentCandidate(candidate); ok {
			sessions = append(sessions, session)
		}
	}
	sort.Strings(providers)
	providers = dedupeStrings(providers)
	return AgentSessionScan{Sessions: sessions, ScannedFiles: uint32(scanned), Truncated: truncated, Providers: providers}, nil
}

func agentProviderRoots(home string) []agentProviderRoot {
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" || !filepath.IsAbs(codexHome) {
		codexHome = filepath.Join(home, ".codex")
	}
	return []agentProviderRoot{
		{"codex", filepath.Join(codexHome, "sessions")},
		{"claude", filepath.Join(home, ".claude", "projects")},
		{"gemini", filepath.Join(home, ".gemini", "tmp")},
		{"cursor", filepath.Join(home, ".cursor", "projects")},
		{"opencode", filepath.Join(home, ".local", "share", "opencode", "storage", "session")},
		{"copilot", filepath.Join(home, ".copilot", "session-state")},
		{"pi", filepath.Join(home, ".pi", "agent", "sessions")},
		{"hermes", filepath.Join(home, ".hermes", "sessions")},
		{"openclaw", filepath.Join(home, ".openclaw", "agents")},
		{"droid", filepath.Join(home, ".factory", "sessions")},
		{"kimi", filepath.Join(home, ".kimi", "sessions")},
	}
}

func discoverAgentFiles(root agentProviderRoot, output *[]agentCandidate, truncated *bool) error {
	type pendingDir struct {
		path  string
		depth int
	}
	pending := []pendingDir{{root.path, 0}}
	for len(pending) > 0 {
		if len(*output) >= maxAgentSessionFiles {
			*truncated = true
			return nil
		}
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current.depth > maxAgentSessionDepth {
			*truncated = true
			continue
		}
		entries, err := os.ReadDir(current.path)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				continue
			}
			return fmt.Errorf("read agent session directory failed: %w", err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]
			path := filepath.Join(current.path, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				pending = append(pending, pendingDir{path, current.depth + 1})
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || !isAgentSessionFile(path) {
				continue
			}
			modified := float64(0)
			if stamp := info.ModTime(); !stamp.IsZero() {
				modified = float64(stamp.UnixMilli())
			}
			*output = append(*output, agentCandidate{root.provider, path, modified, float64(info.Size())})
			if len(*output) >= maxAgentSessionFiles {
				*truncated = true
				return nil
			}
		}
	}
	return nil
}

func isAgentSessionFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".jsonl", ".ndjson":
		return true
	default:
		return false
	}
}

type agentProjection struct {
	sessionID    string
	title        string
	cwd          string
	firstPrompt  string
	messageCount uint32
}

func projectAgentCandidate(candidate agentCandidate) (AgentSession, bool) {
	file, err := os.Open(candidate.path)
	if err != nil {
		return AgentSession{}, false
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxAgentPrefixBytes))
	if err != nil {
		return AgentSession{}, false
	}
	projection := projectAgentTranscript(string(content))
	fallback := strings.TrimSpace(strings.TrimSuffix(filepath.Base(candidate.path), filepath.Ext(candidate.path)))
	if fallback == "" {
		return AgentSession{}, false
	}
	sessionID := projection.sessionID
	if sessionID == "" {
		sessionID = fallback
	}
	title := projection.title
	if title == "" {
		title = projection.firstPrompt
	}
	title = oneLineAgent(title, 100)
	if title == "" {
		title = sessionID
	}
	var cwd *string
	if projection.cwd != "" {
		value := projection.cwd
		cwd = &value
	}
	var prompt *string
	if projection.firstPrompt != "" {
		value := boundAgentText(projection.firstPrompt, 600)
		prompt = &value
	}
	return AgentSession{Provider: candidate.provider, SessionID: sessionID, Title: title, CWD: cwd, FirstPrompt: prompt, FilePath: candidate.path, ModifiedAt: candidate.modifiedAt, SizeBytes: candidate.sizeBytes, MessageCount: projection.messageCount}, true
}

func projectAgentTranscript(content string) agentProjection {
	projection := agentProjection{}
	var value any
	if json.Unmarshal([]byte(content), &value) == nil {
		projectAgentValue(value, &projection)
		return projection
	}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for line := 0; line < maxAgentJSONLines && scanner.Scan(); line++ {
		var item any
		if json.Unmarshal(scanner.Bytes(), &item) == nil {
			projectAgentValue(item, &projection)
		}
	}
	return projection
}

func projectAgentValue(value any, projection *agentProjection) {
	if projection.sessionID == "" {
		projection.sessionID = agentSessionIdentifier(value)
	}
	if projection.title == "" {
		projection.title = findAgentString(value, "title", "summary", "session_title")
		if !sensibleAgentMetadata(projection.title) {
			projection.title = ""
		}
	}
	if projection.cwd == "" {
		candidate := findAgentString(value, "cwd", "working_directory", "project_path", "workspace_path")
		if filepath.IsAbs(candidate) {
			projection.cwd = candidate
		}
	}
	if prompt, ok := agentUserMessage(value); ok {
		projection.messageCount++
		if projection.firstPrompt == "" {
			projection.firstPrompt = prompt
		}
	} else if agentHasAssistantRole(value) {
		projection.messageCount++
	}
}

func agentSessionIdentifier(value any) string {
	for _, key := range []string{"session_id", "sessionId", "conversation_id", "conversationId"} {
		candidate := findAgentString(value, key)
		if sensibleAgentIdentifier(candidate) {
			return candidate
		}
	}
	if object, ok := value.(map[string]any); ok && object["type"] == "session_meta" {
		if payload, ok := object["payload"]; ok {
			for _, key := range []string{"id", "session_id"} {
				candidate := findAgentString(payload, key)
				if sensibleAgentIdentifier(candidate) {
					return candidate
				}
			}
		}
	}
	return ""
}

func agentUserMessage(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		if list, ok := value.([]any); ok {
			for _, item := range list {
				if text, found := agentUserMessage(item); found {
					return text, true
				}
			}
		}
		return "", false
	}
	role, _ := object["role"].(string)
	kind, _ := object["type"].(string)
	if strings.EqualFold(role, "user") || kind == "user" || kind == "human" || kind == "user_message" {
		for _, key := range []string{"content", "message", "text", "prompt"} {
			if text := agentMessageText(object[key]); text != "" {
				return text, true
			}
		}
	}
	for _, nested := range object {
		if text, found := agentUserMessage(nested); found {
			return text, true
		}
	}
	return "", false
}

func agentMessageText(value any) string {
	switch typed := value.(type) {
	case string:
		return nonEmptyAgentText(typed)
	case []any:
		for _, item := range typed {
			if text := agentMessageText(item); text != "" {
				return text
			}
		}
	case map[string]any:
		for _, key := range []string{"text", "content", "prompt"} {
			if text := agentMessageText(typed[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func agentHasAssistantRole(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if role, _ := typed["role"].(string); strings.EqualFold(role, "assistant") {
			return true
		}
		for _, nested := range typed {
			if agentHasAssistantRole(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if agentHasAssistantRole(nested) {
				return true
			}
		}
	}
	return false
}

func findAgentString(value any, keys ...string) string {
	object, ok := value.(map[string]any)
	if ok {
		for _, key := range keys {
			if text, ok := object[key].(string); ok && nonEmptyAgentText(text) != "" {
				return strings.TrimSpace(text)
			}
		}
		for _, nested := range object {
			if text := findAgentString(nested, keys...); text != "" {
				return text
			}
		}
	}
	if list, ok := value.([]any); ok {
		for _, nested := range list {
			if text := findAgentString(nested, keys...); text != "" {
				return text
			}
		}
	}
	return ""
}

func nonEmptyAgentText(value string) string { return strings.TrimSpace(value) }

func sensibleAgentIdentifier(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= 256 && !strings.ContainsAny(trimmed, " \t\r\n")
}

func sensibleAgentMetadata(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= 400
}

func oneLineAgent(value string, limit int) string {
	return boundAgentText(strings.Join(strings.Fields(value), " "), limit)
}

func boundAgentText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func dedupeStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	output := values[:1]
	for _, value := range values[1:] {
		if value != output[len(output)-1] {
			output = append(output, value)
		}
	}
	return output
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
