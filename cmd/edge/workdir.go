package edge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// nodeMeta is what we persist alongside each node's workdir. The
// registration_token is the secret bit — sha256 lives on the backend
// after first connect, so this file is the only place the raw value
// exists after `everyapi edge register`. File mode 0600 + parent dir
// 0700; XDG-style location keeps it out of the cwd.
type nodeMeta struct {
	NodeID            int    `json:"node_id"`
	NodeName          string `json:"node_name"`
	RegistrationToken string `json:"registration_token"`
	Gateway           string `json:"gateway"`
	Mode              Mode   `json:"mode,omitempty"`
}

// dataRoot returns ~/.local/share/everyapi/edge (or the XDG override).
// XDG_DATA_HOME wins if set so a sandboxed test can point at a temp
// dir without monkeypatching the home dir.
func dataRoot() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "everyapi", "edge"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "everyapi", "edge"), nil
}

// configRoot returns ~/.config/everyapi/edge (or the XDG override).
// Used for the `active` pointer file — small ephemeral state that
// doesn't belong next to the per-node data directories.
func configRoot() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "everyapi", "edge"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "everyapi", "edge"), nil
}

func nodeDir(id int) (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, strconv.Itoa(id)), nil
}

// activeNodeID reads ~/.config/everyapi/edge/active and parses the
// stored node id. Returns ErrNoActiveNode if the file is missing —
// caller can render a helpful "register first" message.
var errNoActiveNode = errors.New("no active edge node — run 'everyapi edge register' first")

func activeNodeID() (int, error) {
	cfg, err := configRoot()
	if err != nil {
		return 0, err
	}
	b, err := os.ReadFile(filepath.Join(cfg, "active"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, errNoActiveNode
		}
		return 0, err
	}
	id, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("active node file is malformed: %s", string(b))
	}
	return id, nil
}

func setActiveNodeID(id int) error {
	cfg, err := configRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg, "active"), []byte(strconv.Itoa(id)+"\n"), 0o600)
}

func clearActiveNodeID(id int) error {
	cfg, err := configRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(cfg, "active")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cur, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	// Only clear if the pointer matches — don't accidentally unset
	// the active node when removing a sibling.
	if cur == id {
		return os.Remove(path)
	}
	return nil
}

// resolveNodeID returns the node id from the explicit flag if non-zero,
// else falls back to the active node. Centralises the "which node am
// I operating on" decision so every subcommand handles missing-active
// the same way.
func resolveNodeID(explicit int) (int, error) {
	if explicit > 0 {
		return explicit, nil
	}
	return activeNodeID()
}
