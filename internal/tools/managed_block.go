package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Managed-block markers. A client whose only documented instruction surface is a file the USER owns cannot be given a process-scoped copy — there is nowhere else it will look. For those, EveryAPI writes a delimited block into that file at launch and removes exactly that block at exit, leaving everything the user wrote untouched on both passes.
//
// The markers are HTML comments because every surface this applies to is markdown or plain text, where a comment is inert.
const (
	managedBlockBegin = "<!-- everyapi:begin — managed by `everyapi use`; removed when the launch exits -->"
	managedBlockEnd   = "<!-- everyapi:end -->"
)

// managedBlockMarker carries the paths TakeManagedBlockCleanup must un-patch, newline-separated, through the environment map the adapters return. It travels in the launch environment itself, so `use` takes it out of that map — after Prepare's overlay is merged in — rather than out of the overlay.
const managedBlockMarker = "__EVERYAPI_MANAGED_BLOCKS"

// writeManagedBlock inserts or replaces EveryAPI's block in path, preserving every other line and the file's existing permissions.
//
// It deliberately refuses to create the parent directory. These paths belong to the client's own configuration; if the directory is not there, the user does not use that client's global configuration, and conjuring one is a side effect nobody asked for.
func writeManagedBlock(path, body string) error {
	if body == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return nil
	}
	existing, mode, err := readManagedBlockTarget(path)
	if err != nil {
		return err
	}
	block := managedBlockBegin + "\n" + body + "\n" + managedBlockEnd
	updated := strings.TrimRight(stripManagedBlock(existing), "\n")
	if updated == "" {
		updated = block + "\n"
	} else {
		updated = updated + "\n\n" + block + "\n"
	}
	return writeFileAtomic(path, []byte(updated), mode)
}

// removeManagedBlock takes EveryAPI's block back out. A file that held nothing else is deleted rather than left behind empty — it did not exist before the launch.
func removeManagedBlock(path string) error {
	existing, mode, err := readManagedBlockTarget(path)
	if err != nil || existing == "" {
		return err
	}
	stripped := stripManagedBlock(existing)
	if strings.TrimSpace(stripped) == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFileAtomic(path, []byte(strings.TrimRight(stripped, "\n")+"\n"), mode)
}

// readManagedBlockTarget returns a file's content and the mode to rewrite it with. A missing file reads as empty with a private default, so a first write does not widen anything.
func readManagedBlockTarget(path string) (string, os.FileMode, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", 0o600, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%s is not a regular file", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("read %s: %w", path, err)
	}
	return string(body), info.Mode().Perm(), nil
}

// stripManagedBlock removes every managed block from s. Every block, not the first: a launch killed hard enough to skip its cleanup leaves one behind, and the next launch has to converge on exactly one rather than stack a second.
//
// An unterminated opening marker — a file truncated mid-write — takes the rest of the file with it. That content is EveryAPI's own; the alternative is leaving a permanent half-block in the user's file.
func stripManagedBlock(s string) string {
	for {
		start := strings.Index(s, managedBlockBegin)
		if start < 0 {
			return s
		}
		rest := s[start+len(managedBlockBegin):]
		end := strings.Index(rest, managedBlockEnd)
		if end < 0 {
			return strings.TrimRight(s[:start], "\n")
		}
		after := start + len(managedBlockBegin) + end + len(managedBlockEnd)
		s = strings.TrimRight(s[:start], "\n") + strings.TrimLeft(s[after:], "\n")
	}
}

// applyManagedBlocks writes the agent context into each user-owned path and records them for cleanup. Returns the marker value, or "" when nothing was written.
func applyManagedBlocks(paths ...string) string {
	body := agentContextBody()
	if body == "" {
		return ""
	}
	var written []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		// A failure here must not fail the launch: the client still works, it just does not know about the handbook.
		if err := writeManagedBlock(path, body); err != nil {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			written = append(written, path)
		}
	}
	return strings.Join(written, "\n")
}

// TakeManagedBlockCleanup removes the internal marker before the child receives its environment and returns the un-patch for every file this launch touched.
func TakeManagedBlockCleanup(env map[string]string) func() {
	encoded := env[managedBlockMarker]
	delete(env, managedBlockMarker)
	if encoded == "" {
		return nil
	}
	paths := strings.Split(encoded, "\n")
	return func() {
		for _, path := range paths {
			if path != "" {
				_ = removeManagedBlock(path)
			}
		}
	}
}

// gooseHintsPath resolves Goose's documented global hints file, `~/.config/goose/.goosehints`, honouring XDG_CONFIG_HOME.
//
// It returns "" when that directory does not already exist, which is the whole safety margin on this path: writeManagedBlock will not create it either, so a machine where Goose has never stored global configuration is left exactly as it was.
func gooseHintsPath() string {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".config")
	}
	dir := filepath.Join(root, "goose")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Join(dir, ".goosehints")
}
