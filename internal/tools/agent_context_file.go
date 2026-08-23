package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Context files EveryAPI generates inside a prepared home.
//
// Two names, one content. EVERYAPI.md is the honest label — a user who looks
// inside a prepared home can tell whose file it is, and adapters that point at
// a path by configuration point at this one. AGENTS.md is the cross-tool
// convention several clients read by default (Goose lists it ahead of
// .goosehints; Gemini CLI ships it in the documented context.fileName example),
// so writing it costs one extra file and covers clients whose own surface we
// have not wired by name.
//
// Writing AGENTS.md is best-effort reach, not a support claim: a client that
// does not read the convention simply ignores a file in a directory that is
// deleted when it exits.
const (
	agentContextFileName = "EVERYAPI.md"
	agentsConventionName = "AGENTS.md"
)

// disableEnv opts a launch out of every form of context injection.
const disableEnv = "EVERYAPI_NO_AGENT_CONTEXT"

// agentContextFileEnabled reports whether this launch should inject anything.
func agentContextFileEnabled() bool { return os.Getenv(disableEnv) == "" }

// agentContextBody is the text every surface carries, or "" when injection is off.
func agentContextBody() string {
	if !agentContextFileEnabled() {
		return ""
	}
	return AgentInstructions()
}

// writeAgentContextFile drops the instructions into dir under both names and returns the EVERYAPI.md path, or "" when there is nothing to write.
//
// Every caller passes a PROCESS-SCOPED directory, and that is the constraint that decides whether a client can be wired at all. `~/.config/goose/.goosehints`, `~/.gemini/GEMINI.md`, and a repository's own AGENTS.md are the user's files; a launcher that edits them leaves the machine changed after the launch it was supposed to scope. A client whose only documented surface is one of those is handled by the managed-block path instead, which removes what it wrote.
func writeAgentContextFile(dir string) (string, error) {
	body := agentContextBody()
	if body == "" || dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create agent context directory: %w", err)
	}
	path := filepath.Join(dir, agentContextFileName)
	if err := writeFileAtomic(path, []byte(body+"\n"), 0o600); err != nil {
		return "", err
	}
	// Best-effort convention alias. A failure here must not fail a launch whose real surface is the path above.
	_ = writeFileAtomic(filepath.Join(dir, agentsConventionName), []byte(body+"\n"), 0o600)
	return path, nil
}

// addAgentContextToHome writes the context files into an existing prepared home. For clients that read a convention file out of their own home and need no configuration entry pointing at it.
func addAgentContextToHome(home string) {
	if home == "" {
		return
	}
	// Best-effort by construction: this is reach beyond a client's named surface, so a write failure leaves the launch exactly as it would have been without it.
	_, _ = writeAgentContextFile(home)
}

// agentContextArgv builds the argv the launcher prepends for a client whose documented surface is a command-line option — `aider --read <file>` and anything shaped like it. It creates the prepared home, writes the file, and returns the home plus the encoded argv, or ("", "") when injection is off.
func agentContextArgv(prefix, flag string) (home, encodedArgv string, err error) {
	if agentContextBody() == "" {
		return "", "", nil
	}
	home, err = newPreparedHome(prefix)
	if err != nil {
		return "", "", err
	}
	// newPreparedHome has already written both context files into the home; this only needs the path to point the flag at, and confirmation that the write actually landed.
	path := filepath.Join(home, agentContextFileName)
	if _, err := os.Stat(path); err != nil {
		removePreparedHomeAfterQuiet(home)
		return "", "", fmt.Errorf("agent context file was not created: %w", err)
	}
	args, err := json.Marshal([]string{flag, path})
	if err != nil {
		removePreparedHomeAfterQuiet(home)
		return "", "", fmt.Errorf("encode %s runtime arguments: %w", prefix, err)
	}
	return home, string(args), nil
}
