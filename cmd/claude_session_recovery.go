package cmd

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	claudeTerminalToolParseFailure = "The model's tool call could not be parsed (retry also failed)."
	claudePollutionClusterMaxGap   = 5 * time.Minute
)

var (
	claudeInlineCodePattern = regexp.MustCompile("`[^`\\n]*`")
	claudeSessionIDPattern  = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)
)

type claudeSessionPollution struct {
	FirstLine        int
	FirstMessageID   string
	FirstTimestamp   string
	AffectedMessages int
	SessionCWD       string
	// SelfRecovered marks a transcript whose polluted burst is followed by a
	// substantial clean tail: the session demonstrably recovered on its own,
	// so forking at the first pollution would discard real work.
	SelfRecovered bool
}

type claudeSessionRecovery struct {
	OriginalSessionID string
	NewSessionID      string
	Pollution         *claudeSessionPollution
	// GuardOnly resumes the polluted session unchanged (it self-recovered
	// with a substantial clean tail) and only arms the response guard.
	GuardOnly bool
	// Reused redirects the resume to the clean clone minted by an earlier
	// recovery of the same session instead of creating another clone.
	Reused bool
	// CreatedClone marks that this launch minted CleanPath (and MarkerPath,
	// when non-empty) so an abandoned launch can remove them again.
	CreatedClone bool
	CleanPath    string
	MarkerPath   string
}

type claudeTranscriptRecord struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	CWD         string `json:"cwd"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Message     struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeTranscriptStateRecord struct {
	SessionID  string `json:"sessionId"`
	Type       string `json:"type"`
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parentUuid"`
	Attachment struct {
		Type      string `json:"type"`
		Condition string `json:"condition"`
		Met       bool   `json:"met"`
		Sentinel  bool   `json:"sentinel"`
	} `json:"attachment"`
}

type claudeAssistantGroup struct {
	messageID  string
	firstLine  int
	timestamp  string
	stopReason string
	text       strings.Builder
	weak       bool
	strong     bool
	standalone int
	userTurn   int
}

func prepareClaudeSessionRecovery(
	args []string,
	claudeDir string,
	generateSessionID func() (string, error),
) ([]string, *claudeSessionRecovery) {
	if _, _, _, ok := explicitClaudeResume(args); !ok {
		return args, nil
	}
	newSessionID, err := generateSessionID()
	if err != nil {
		return args, nil
	}
	rewritten, recovery, err := recoverClaudeResume(args, claudeDir, newSessionID)
	if err != nil {
		return args, nil
	}
	return rewritten, recovery
}

func detectClaudeSessionPollution(path string) (*claudeSessionPollution, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var groups []claudeAssistantGroup
	groupByMessageID := make(map[string]int)
	sessionCWD := ""
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	lineNo := 0
	userTurn := 0
	for scanner.Scan() {
		lineNo++
		var rec claudeTranscriptRecord
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("parse Claude transcript line %d: %w", lineNo, err)
		}
		if rec.IsSidechain {
			continue
		}
		if sessionCWD == "" && rec.CWD != "" {
			sessionCWD = rec.CWD
		}
		switch rec.Type {
		case "user":
			if rec.IsMeta {
				continue
			}
			text, err := claudeTextContent(rec.Message.Content)
			if err != nil {
				return nil, fmt.Errorf("parse Claude transcript line %d message content: %w", lineNo, err)
			}
			if text != "" {
				userTurn++
			}
		case "assistant":
			text, err := claudeTextContent(rec.Message.Content)
			if err != nil {
				return nil, fmt.Errorf("parse Claude transcript line %d message content: %w", lineNo, err)
			}
			messageID := rec.Message.ID
			if messageID == "" {
				messageID = "line:" + strconv.Itoa(lineNo)
			}
			idx, ok := groupByMessageID[messageID]
			if !ok {
				idx = len(groups)
				groupByMessageID[messageID] = idx
				groups = append(groups, claudeAssistantGroup{
					messageID: messageID,
					firstLine: lineNo,
					timestamp: rec.Timestamp,
					userTurn:  userTurn,
				})
			}
			group := &groups[idx]
			if group.stopReason == "" {
				group.stopReason = rec.Message.StopReason
			}
			if text != "" {
				if group.text.Len() > 0 {
					group.text.WriteByte('\n')
				}
				group.text.WriteString(text)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	strongIndex := -1
	clusterStart := -1
	for i := range groups {
		classifyClaudeAssistantGroup(&groups[i])
	}
	for i := range groups {
		if groups[i].strong {
			strongIndex = i
			break
		}
		if first, ok := claudeStandaloneClusterStart(groups, i); ok {
			strongIndex = i
			clusterStart = first
			break
		}
	}
	if strongIndex < 0 {
		return nil, nil
	}

	// A cluster confirmation implicates every contributing member, so the
	// boundary starts at the EARLIEST member — not the trigger index — even
	// when clean groups are interleaved inside the window.
	start := strongIndex
	if clusterStart >= 0 && clusterStart < start {
		start = clusterStart
	}
	cleanGap := 0
	for i := start - 1; i >= 0 && strongIndex-i <= 8; i-- {
		if !claudeGroupsSharePollutionWindow(groups[i], groups[strongIndex]) {
			break
		}
		if groups[i].weak {
			start = i
			cleanGap = 0
			continue
		}
		cleanGap++
		if cleanGap > 1 {
			break
		}
	}

	result := &claudeSessionPollution{
		FirstLine:      groups[start].firstLine,
		FirstMessageID: groups[start].messageID,
		FirstTimestamp: groups[start].timestamp,
		SessionCWD:     sessionCWD,
	}
	lastAffected := strongIndex
	for i := len(groups) - 1; i >= start; i-- {
		if groups[i].weak || groups[i].strong {
			lastAffected = i
			break
		}
	}
	for i := start; i < len(groups); i++ {
		if groups[i].weak || groups[i].strong {
			result.AffectedMessages++
		}
	}
	// A polluted burst followed by a later user turn whose assistant output
	// stayed entirely clean means the session recovered on its own; forking
	// at the first pollution would silently discard that later work.
	tailClean := len(groups) - 1 - lastAffected
	result.SelfRecovered = tailClean >= 3 &&
		groups[len(groups)-1].userTurn > groups[lastAffected].userTurn
	return result, nil
}

func recoverClaudeResume(args []string, claudeDir, newSessionID string) ([]string, *claudeSessionRecovery, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return args, nil, nil
	}
	return recoverClaudeResumeFromDir(args, claudeDir, currentDir, newSessionID)
}

func recoverClaudeResumeFromDir(args []string, claudeDir, currentDir, newSessionID string) ([]string, *claudeSessionRecovery, error) {
	originalSessionID, _, _, ok := explicitClaudeResume(args)
	if !ok || claudeDir == "" || !claudeSessionIDPattern.MatchString(originalSessionID) {
		return args, nil, nil
	}
	path, ok := findClaudeSessionPath(claudeDir, originalSessionID)
	if !ok {
		return args, nil, nil
	}
	// Follow markers left by earlier recoveries of this session, so a
	// replayed `--resume <original>` (shell history, muscle memory) reuses
	// the existing clone chain instead of minting another full copy per
	// launch. Bounded so a corrupt marker cycle can't loop forever.
	resumeID := originalSessionID
	for hop := 0; hop < 8; hop++ {
		next, ok := readClaudeRecoveryMarker(filepath.Dir(path), resumeID)
		if !ok {
			break
		}
		nextPath := filepath.Join(filepath.Dir(path), next+".jsonl")
		if _, statErr := os.Stat(nextPath); statErr != nil {
			break
		}
		resumeID = next
		path = nextPath
	}
	pollution, err := detectClaudeSessionPollution(path)
	if err != nil {
		return args, nil, err
	}
	if pollution == nil {
		if resumeID == originalSessionID {
			return args, nil, nil
		}
		rewritten, ok := rewriteClaudeClonedResumeArgs(args, resumeID)
		if !ok {
			return args, nil, nil
		}
		return rewritten, &claudeSessionRecovery{
			OriginalSessionID: originalSessionID,
			NewSessionID:      resumeID,
			Reused:            true,
		}, nil
	}
	if pollution.SessionCWD == "" || !sameClaudeWorkingDirectory(pollution.SessionCWD, currentDir) {
		return args, nil, nil
	}
	if pollution.SelfRecovered {
		// The polluted burst is followed by substantial clean work under a
		// later user turn: the session demonstrably recovered on its own.
		// Truncating at the first pollution would silently discard that
		// work, so resume the transcript unchanged and only arm the
		// response guard against a recurrence.
		rewritten := args
		reused := false
		if resumeID != originalSessionID {
			rewritten, ok = rewriteClaudeClonedResumeArgs(args, resumeID)
			if !ok {
				return args, nil, nil
			}
			reused = true
		}
		return rewritten, &claudeSessionRecovery{
			OriginalSessionID: originalSessionID,
			NewSessionID:      resumeID,
			Pollution:         pollution,
			GuardOnly:         true,
			Reused:            reused,
		}, nil
	}
	rewritten, ok := rewriteClaudeClonedResumeArgs(args, newSessionID)
	if !ok {
		return args, nil, nil
	}
	cleanPath, err := cloneClaudeSessionPrefix(
		path,
		resumeID,
		newSessionID,
		pollution.FirstLine,
	)
	if err != nil {
		return args, nil, err
	}
	// Best-effort: losing the marker only costs clone reuse on a later
	// launch, never correctness of this one.
	markerPath := writeClaudeRecoveryMarker(filepath.Dir(path), resumeID, newSessionID)
	return rewritten, &claudeSessionRecovery{
		OriginalSessionID: originalSessionID,
		NewSessionID:      newSessionID,
		Pollution:         pollution,
		CreatedClone:      true,
		CleanPath:         cleanPath,
		MarkerPath:        markerPath,
	}, nil
}

// discard removes the artifacts minted for an abandoned launch (guard
// startup failure, yolo-prompt cancel, Prepare error) so it doesn't leave a
// phantom resumable session in Claude Code's picker. A no-op unless this
// recovery actually created a clone.
func (r *claudeSessionRecovery) discard() {
	if r == nil || !r.CreatedClone {
		return
	}
	if r.CleanPath != "" {
		_ = os.Remove(r.CleanPath)
	}
	if r.MarkerPath != "" {
		_ = os.Remove(r.MarkerPath)
	}
}

// claudeRecoveryMarkerPath names the sidecar that records where a polluted
// session was recovered to. The dotfile prefix and non-.jsonl suffix keep it
// out of Claude Code's session globs.
func claudeRecoveryMarkerPath(dir, sessionID string) string {
	return filepath.Join(dir, "."+sessionID+".everyapi-recovery")
}

func readClaudeRecoveryMarker(dir, sessionID string) (string, bool) {
	data, err := os.ReadFile(claudeRecoveryMarkerPath(dir, sessionID))
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(data))
	if !claudeSessionIDPattern.MatchString(id) {
		return "", false
	}
	return id, true
}

// writeClaudeRecoveryMarker records fromID → toID and returns the marker
// path, or "" when the write failed (reuse is best-effort).
func writeClaudeRecoveryMarker(dir, fromID, toID string) string {
	path := claudeRecoveryMarkerPath(dir, fromID)
	if err := os.WriteFile(path, []byte(toID+"\n"), 0o600); err != nil {
		return ""
	}
	return path
}

func explicitClaudeResume(args []string) (sessionID string, start, end int, ok bool) {
	found := false
	for i, arg := range args {
		switch {
		case arg == "--":
			return sessionID, start, end, found
		case arg == "--continue" || arg == "-c":
			return "", 0, 0, false
		case arg == "--resume" || arg == "-r":
			if found || i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return "", 0, 0, false
			}
			sessionID, start, end, found = args[i+1], i, i+2, true
		case strings.HasPrefix(arg, "--resume="):
			id := strings.TrimPrefix(arg, "--resume=")
			if found || id == "" {
				return "", 0, 0, false
			}
			sessionID, start, end, found = id, i, i+1, true
		}
	}
	return sessionID, start, end, found
}

func findClaudeSessionPath(claudeDir, sessionID string) (string, bool) {
	paths, err := filepath.Glob(filepath.Join(claudeDir, "projects", "*", sessionID+".jsonl"))
	if err != nil || len(paths) != 1 {
		return "", false
	}
	return paths[0], true
}

func sameClaudeWorkingDirectory(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		return os.SameFile(aInfo, bInfo)
	}
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return filepath.Clean(aAbs) == filepath.Clean(bAbs)
}

// cloneClaudeSessionPrefix preserves the complete JSONL prefix before the
// first polluted record, changing only top-level sessionId fields. It writes a
// private temporary file and publishes it with an atomic no-replace hard link.
func cloneClaudeSessionPrefix(
	sourcePath, originalSessionID, newSessionID string,
	firstPollutedLine int,
) (cleanPath string, err error) {
	if firstPollutedLine <= 1 || !claudeSessionIDPattern.MatchString(originalSessionID) ||
		!claudeSessionIDPattern.MatchString(newSessionID) {
		return "", fmt.Errorf("invalid Claude session clone boundary or ID")
	}

	cleanPath = filepath.Join(filepath.Dir(sourcePath), newSessionID+".jsonl")
	if _, statErr := os.Stat(cleanPath); statErr == nil {
		return "", fmt.Errorf("clean Claude session already exists: %s", cleanPath)
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer source.Close()

	temp, err := os.CreateTemp(filepath.Dir(sourcePath), "."+newSessionID+".*.tmp")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			temp.Close()
			os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return "", err
	}

	writer := bufio.NewWriter(temp)
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	copiedLines := 0
	scannedLines := 0
	lastCleanUUID := ""
	activeGoals := make(map[string]string)
	completedGoals := make(map[string][]byte)
	for scanner.Scan() {
		scannedLines++
		lineNo := scannedLines
		line := append([]byte(nil), scanner.Bytes()...)
		var state claudeTranscriptStateRecord
		if len(bytes.TrimSpace(line)) > 0 {
			if err := json.Unmarshal(line, &state); err != nil {
				return "", fmt.Errorf("parse Claude transcript line %d: %w", lineNo, err)
			}
		}

		if lineNo >= firstPollutedLine {
			if state.Type == "attachment" && state.Attachment.Type == "goal_status" &&
				state.Attachment.Met && activeGoals[state.Attachment.Condition] != "" &&
				completedGoals[state.Attachment.Condition] == nil {
				completedGoals[state.Attachment.Condition] = line
			}
			continue
		}

		if state.UUID != "" {
			lastCleanUUID = state.UUID
		}
		if state.Type == "attachment" && state.Attachment.Type == "goal_status" {
			switch {
			case state.Attachment.Met:
				delete(activeGoals, state.Attachment.Condition)
			case state.Attachment.Sentinel:
				activeGoals[state.Attachment.Condition] = state.UUID
			}
		}
		if len(bytes.TrimSpace(line)) > 0 {
			line, err = rewriteClaudeTranscriptSessionID(line, originalSessionID, newSessionID)
			if err != nil {
				return "", fmt.Errorf("rewrite Claude transcript line %d: %w", lineNo, err)
			}
		}
		if _, err := writer.Write(line); err != nil {
			return "", err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return "", err
		}
		copiedLines++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if copiedLines != firstPollutedLine-1 {
		return "", fmt.Errorf("Claude transcript changed while cloning clean prefix")
	}

	// A session-scoped /goal can start before the first malformed response
	// and complete later. Cutting only the clean prefix would resurrect that
	// already-finished Stop hook on resume. Carry over only its minimal
	// completion metadata (never assistant/user content or the evaluator's
	// free-form reason), reparented onto the clean transcript chain.
	if lastCleanUUID != "" {
		for _, condition := range sortedClaudeGoalConditions(activeGoals) {
			completion := completedGoals[condition]
			if completion == nil {
				completion = findClaudeGoalCompletionInProvenance(
					filepath.Dir(sourcePath),
					sourcePath,
					condition,
					activeGoals[condition],
				)
			}
			if completion == nil {
				continue
			}
			rewrittenCompletion, completionUUID, rewriteErr := rewriteClaudeGoalCompletion(
				completion,
				newSessionID,
				lastCleanUUID,
			)
			if rewriteErr != nil {
				return "", rewriteErr
			}
			if _, err := writer.Write(rewrittenCompletion); err != nil {
				return "", err
			}
			if err := writer.WriteByte('\n'); err != nil {
				return "", err
			}
			if completionUUID != "" {
				lastCleanUUID = completionUUID
			}
		}
	}
	if err := writer.Flush(); err != nil {
		return "", err
	}
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := publishClaudeCleanSession(tempPath, cleanPath); err != nil {
		return "", err
	}
	committed = true
	return cleanPath, nil
}

func sortedClaudeGoalConditions(active map[string]string) []string {
	conditions := make([]string, 0, len(active))
	for condition := range active {
		conditions = append(conditions, condition)
	}
	sort.Strings(conditions)
	return conditions
}

func findClaudeGoalCompletionInProvenance(dir, sourcePath, condition, activationUUID string) []byte {
	if activationUUID == "" || condition == "" {
		return nil
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil
	}
	for _, path := range paths {
		if path == sourcePath {
			continue
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		foundActivation := false
		var completion []byte
		for scanner.Scan() {
			line := scanner.Bytes()
			if !bytes.Contains(line, []byte(`"goal_status"`)) {
				continue
			}
			var state claudeTranscriptStateRecord
			if json.Unmarshal(line, &state) != nil || state.Type != "attachment" ||
				state.Attachment.Type != "goal_status" || state.Attachment.Condition != condition {
				continue
			}
			if state.UUID == activationUUID && state.Attachment.Sentinel && !state.Attachment.Met {
				foundActivation = true
				continue
			}
			if foundActivation && state.Attachment.Met {
				completion = append([]byte(nil), line...)
				break
			}
		}
		_ = f.Close()
		if completion != nil {
			return completion
		}
	}
	return nil
}

func rewriteClaudeGoalCompletion(line []byte, newSessionID, parentUUID string) ([]byte, string, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(line, &record); err != nil {
		return nil, "", err
	}
	var state claudeTranscriptStateRecord
	if err := json.Unmarshal(line, &state); err != nil {
		return nil, "", err
	}
	if state.Type != "attachment" || state.Attachment.Type != "goal_status" ||
		!state.Attachment.Met || state.Attachment.Condition == "" {
		return nil, "", fmt.Errorf("invalid Claude goal completion record")
	}
	if _, ok := record["sessionId"]; !ok || state.SessionID == "" {
		return nil, "", fmt.Errorf("Claude goal completion missing session ID")
	}
	record["sessionId"], _ = json.Marshal(newSessionID)
	record["parentUuid"], _ = json.Marshal(parentUUID)
	record["attachment"], _ = json.Marshal(map[string]any{
		"type":      "goal_status",
		"met":       true,
		"condition": state.Attachment.Condition,
	})
	out, err := json.Marshal(record)
	return out, state.UUID, err
}

func publishClaudeCleanSession(tempPath, cleanPath string) error {
	if err := os.Link(tempPath, cleanPath); err != nil {
		return err
	}
	if err := os.Remove(tempPath); err != nil {
		_ = os.Remove(cleanPath)
		return err
	}
	return nil
}

func rewriteClaudeTranscriptSessionID(line []byte, originalSessionID, newSessionID string) ([]byte, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(line, &record); err != nil {
		return nil, err
	}
	rawSessionID, ok := record["sessionId"]
	if !ok {
		return append([]byte(nil), line...), nil
	}
	var sessionID string
	if err := json.Unmarshal(rawSessionID, &sessionID); err != nil {
		return nil, err
	}
	if sessionID != originalSessionID {
		return nil, fmt.Errorf("unexpected transcript session ID %q", sessionID)
	}
	newJSON, _ := json.Marshal(newSessionID)
	record["sessionId"] = newJSON
	return json.Marshal(record)
}

func rewriteClaudeClonedResumeArgs(args []string, newSessionID string) ([]string, bool) {
	_, resumeStart, resumeEnd, ok := explicitClaudeResume(args)
	if !ok || !claudeSessionIDPattern.MatchString(newSessionID) {
		return nil, false
	}

	for _, arg := range args[:resumeStart] {
		if arg == "--session-id" || strings.HasPrefix(arg, "--session-id=") {
			return nil, false
		}
	}
	for _, arg := range args[resumeEnd:] {
		if arg == "--" {
			break
		}
		if arg == "--session-id" || strings.HasPrefix(arg, "--session-id=") {
			return nil, false
		}
	}

	result := make([]string, 0, len(args))
	afterEndOfOptions := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			afterEndOfOptions = true
		}
		if i == resumeStart {
			if resumeEnd-resumeStart == 2 {
				result = append(result, args[i], newSessionID)
			} else {
				result = append(result, "--resume="+newSessionID)
			}
			i = resumeEnd - 1
			continue
		}
		if !afterEndOfOptions && args[i] == "--fork-session" {
			continue
		}
		result = append(result, args[i])
	}
	return result, true
}

func newClaudeSessionID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]), nil
}

func classifyClaudeAssistantGroup(group *claudeAssistantGroup) {
	cleaned := stripClaudeMarkdownCode(group.text.String())
	standalone := 0
	for _, line := range strings.Split(cleaned, "\n") {
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "call", "course", "court", "count", "invoke", "parameter", "課":
			standalone++
		}
	}
	hintCount := strings.Count(cleaned, "课件")
	// Match `invoke name="` without requiring the leading `<`: in-the-wild
	// variants of the corruption mangle the opening bracket (e.g. a leaked
	// `antml:invoke name="...` prefix) while the parameter markup survives.
	hasLeakedXML := false
	if invoke := strings.Index(cleaned, `invoke name="`); invoke >= 0 {
		hasLeakedXML = strings.Contains(cleaned[invoke:], `parameter name=`)
	}
	hasTerminalFailure := group.stopReason == "stop_sequence" &&
		strings.Contains(cleaned, claudeTerminalToolParseFailure)
	hasToolContext := group.stopReason == "tool_use" || group.stopReason == "stop_sequence"

	group.standalone = standalone
	group.weak = hintCount > 0 || standalone > 0
	group.strong = hasLeakedXML || hasTerminalFailure || hasToolContext &&
		(standalone >= 3 || hintCount >= 2 && standalone > 0)
}

// claudeStandaloneClusterStart reports whether the window ending at `end`
// confirms a standalone-control-word cluster, and if so returns the index of
// the EARLIEST contributing member so the caller can place the pollution
// boundary before every implicated group (not just the trigger).
func claudeStandaloneClusterStart(groups []claudeAssistantGroup, end int) (int, bool) {
	if end < 0 || end >= len(groups) {
		return -1, false
	}
	windowStart := end - 7
	if windowStart < 0 {
		windowStart = 0
	}
	tokens := 0
	signalGroups := 0
	first := -1
	for i := windowStart; i <= end; i++ {
		group := &groups[i]
		if !claudeGroupsSharePollutionWindow(*group, groups[end]) || group.standalone == 0 ||
			(group.stopReason != "tool_use" && group.stopReason != "stop_sequence") {
			continue
		}
		tokens += group.standalone
		signalGroups++
		if first < 0 {
			first = i
		}
	}
	if tokens >= 3 && signalGroups >= 2 {
		return first, true
	}
	return -1, false
}

func claudeGroupsSharePollutionWindow(start, end claudeAssistantGroup) bool {
	if start.userTurn != end.userTurn {
		return false
	}
	startTime, startErr := time.Parse(time.RFC3339Nano, start.timestamp)
	endTime, endErr := time.Parse(time.RFC3339Nano, end.timestamp)
	return startErr == nil && endErr == nil && !startTime.After(endTime) &&
		endTime.Sub(startTime) <= claudePollutionClusterMaxGap
}

func claudeTextContent(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	switch raw[0] {
	case '"':
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
		return strings.TrimSpace(text), nil
	case '[':
		var blocks []claudeContentBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return "", err
		}
		var parts []string
		for _, block := range blocks {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n")), nil
	default:
		return "", fmt.Errorf("unsupported message content JSON type %q", raw[0])
	}
}

func stripClaudeMarkdownCode(text string) string {
	var kept []string
	fence := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		// Only the marker that opened a fence can close it, so a ~~~ block
		// quoting ``` lines (or vice versa) doesn't flip state mid-fence.
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			fence = "```"
			continue
		}
		if strings.HasPrefix(trimmed, "~~~") {
			fence = "~~~"
			continue
		}
		// Classic markdown indented code blocks are quoted examples too.
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		kept = append(kept, line)
	}
	return claudeInlineCodePattern.ReplaceAllString(strings.Join(kept, "\n"), "")
}
