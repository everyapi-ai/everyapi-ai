package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
)

// Claude Code has no configuration directory of its own under EveryAPI: a launch reuses the user's real ~/.claude, because that is where their skills, global CLAUDE.md, MCP servers, and resumable transcripts live, and prepareClaudeSessionRecovery reads the same tree. Everything a launch changes in there is therefore a change to the user's own installation.
//
// One thing a gateway launch changes that the user did not ask it to: `/model` inside the session writes the pick to `model` in ~/.claude/settings.json as the default for NEW sessions. The picker it was chosen from is the gateway catalogue (CLAUDE_CODE_USE_GATEWAY, see internal/tools/claude.go), so the value can be an id only the relay can route — a marketplace model such as `qwen2.5:7b`, or a bare `opus` that Claude Code's own picker never offers. That value then becomes the default for every ordinary `claude` session the user starts afterwards, where nothing resolves it.
//
// The boot model for a launch is EveryAPI's to choose and it already travels as `--model` (see toolRemembersModel), so an in-session pick is a session-scoped choice by construction. This snapshots the field before the launch and puts it back when the child exits — the same contract managed blocks keep for the user-owned files they patch.

// claudeUserSettingsFile is the file inside the Claude configuration directory that holds the setting.
const claudeUserSettingsFile = "settings.json"

// claudeModelSnapshot is the `model` member of the user's settings as it stood before the launch: whether it was there at all, its verbatim bytes, and the position it occupied, so a restore that has to re-add it puts it back where the user had it rather than at the end.
type claudeModelSnapshot struct {
	path    string
	present bool
	value   json.RawMessage
	index   int
	once    sync.Once
}

// snapshotClaudeUserModel records the pre-launch `model` for claudeDir, or nil when there is nothing that can be restored later: no directory resolved, no readable settings file, or contents that are not a JSON object. Returning nil for unreadable/unparsable input is deliberate — a file this cannot read is a file it must not rewrite.
func snapshotClaudeUserModel(claudeDir string) *claudeModelSnapshot {
	if claudeDir == "" {
		return nil
	}
	path := filepath.Join(claudeDir, claudeUserSettingsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	members, ok := decodeJSONObjectMembers(data)
	if !ok {
		return nil
	}
	snap := &claudeModelSnapshot{path: path, index: len(members)}
	for i, m := range members {
		if m.key != "model" {
			continue
		}
		snap.present = true
		snap.value = m.value
		snap.index = i
		break
	}
	return snap
}

// restore puts the pre-launch value back. It is a no-op when the field still holds what it held before, which is the normal case — a launch where nobody opened the picker must not rewrite the user's settings file at all.
//
// It runs once: ExecWithOptions calls the cleanup chain on both the start-failure and the child-exited paths, and combineCleanups is also reachable from a deferred cleanup, so a second call must not print a second notice.
func (s *claudeModelSnapshot) restore() {
	s.once.Do(s.restoreOnce)
}

func (s *claudeModelSnapshot) restoreOnce() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	members, ok := decodeJSONObjectMembers(data)
	if !ok {
		return
	}
	current := -1
	for i, m := range members {
		if m.key == "model" {
			current = i
			break
		}
	}
	switch {
	case current < 0 && !s.present:
		return
	case current >= 0 && s.present && sameJSONValue(members[current].value, s.value):
		return
	case current >= 0 && !s.present:
		members = append(members[:current], members[current+1:]...)
	case current >= 0:
		members[current].value = s.value
	default:
		at := min(s.index, len(members))
		members = append(members, jsonObjectMember{})
		copy(members[at+1:], members[at:])
		members[at] = jsonObjectMember{key: "model", value: s.value}
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(s.path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := writeFileAtomic(s.path, encodeJSONObjectMembers(members, jsonObjectIndent(data)), mode); err != nil {
		return
	}
	// Said out loud rather than done quietly. A user who opened `/model` during the launch chose something, and finding the choice gone with no explanation reads as the setting failing to save. The line names where the pick does survive.
	cliout.Printf("Restored the `model` field in %s to what it was before this launch. A model picked inside an EveryAPI session belongs to that session; set the launch model with `everyapi use claude --model`.\n", s.path)
}

// jsonObjectMember is one key/value pair of a JSON object, the value kept as the verbatim bytes it had in the source so re-encoding an untouched member cannot reformat it.
type jsonObjectMember struct {
	key   string
	value json.RawMessage
}

// decodeJSONObjectMembers parses a top-level JSON object into its members IN ORDER. encoding/json's map would lose that order, and rewriting a hand-maintained settings file with its keys shuffled is a diff the user never asked for. Reports false for anything that is not a single well-formed object.
func decodeJSONObjectMembers(data []byte) ([]jsonObjectMember, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, false
	}
	if delim, isDelim := tok.(json.Delim); !isDelim || delim != '{' {
		return nil, false
	}
	var members []jsonObjectMember
	for dec.More() {
		keyTok, keyErr := dec.Token()
		if keyErr != nil {
			return nil, false
		}
		key, isString := keyTok.(string)
		if !isString {
			return nil, false
		}
		var value json.RawMessage
		if decErr := dec.Decode(&value); decErr != nil {
			return nil, false
		}
		members = append(members, jsonObjectMember{key: key, value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, false
	}
	// Anything after the object means this is not a file whose members round-trip: re-encoding would silently drop whatever followed. Rejecting it here is what keeps the restore from being a rewrite.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return members, true
}

// encodeJSONObjectMembers renders the members back as an object, one member per line at the given indent, each value emitted as the bytes it arrived with. An object left with no members is written as `{}` rather than deleted: the file existed before the launch.
func encodeJSONObjectMembers(members []jsonObjectMember, indent string) []byte {
	if len(members) == 0 {
		return []byte("{}\n")
	}
	var out bytes.Buffer
	out.WriteString("{\n")
	for i, m := range members {
		key, err := json.Marshal(m.key)
		if err != nil {
			continue
		}
		out.WriteString(indent)
		out.Write(key)
		out.WriteString(": ")
		out.Write(m.value)
		if i < len(members)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("}\n")
	return out.Bytes()
}

// jsonObjectIndent reads the indentation the file already uses off its first member line, so a restore does not convert a tab-indented settings file to spaces on its way past. Falls back to two spaces, which is what Claude Code itself writes.
func jsonObjectIndent(data []byte) string {
	newline := bytes.IndexByte(data, '\n')
	if newline < 0 {
		return "  "
	}
	rest := data[newline+1:]
	width := 0
	for width < len(rest) && (rest[width] == ' ' || rest[width] == '\t') {
		width++
	}
	if width == 0 {
		return "  "
	}
	return string(rest[:width])
}

// sameJSONValue compares two raw values by their canonical form, so whitespace the encoder happened to place inside one of them cannot read as a change the user made.
func sameJSONValue(a, b json.RawMessage) bool {
	var left, right bytes.Buffer
	if json.Compact(&left, a) != nil || json.Compact(&right, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(left.Bytes(), right.Bytes())
}

// writeFileAtomic replaces path through a uniquely named temp file in the same directory, so a launch interrupted mid-write cannot leave the user's settings truncated. The temp name is unique rather than fixed because several everyapi processes can be exiting at once.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
