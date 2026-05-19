package menubar

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// persistedState is menubar-private UI state kept in
// ~/.config/everyapi/menubar-state.json. Lives alongside but
// distinct from credentials.json — credentials are a shared CLI/menubar
// concern, this file is only ever read or written by the menubar
// process. Keeping them separate avoids the CLI accidentally
// migrating, validating, or stomping menubar fields.
//
// Schema is intentionally additive — fields default to their zero
// value when missing, so an older binary that wrote a smaller
// payload still loads cleanly.
type persistedState struct {
	SanitizerEnabled bool   `json:"sanitizer_enabled"`
	SanitizerListen  string `json:"sanitizer_listen,omitempty"`
}

func statePath() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "menubar-state.json"), nil
}

// loadState returns the saved state, or a zero value when no file
// exists. Read errors other than not-found bubble up so a corrupt
// file is visible rather than silently reset.
func loadState() (persistedState, error) {
	path, err := statePath()
	if err != nil {
		return persistedState{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persistedState{}, nil
		}
		return persistedState{}, err
	}
	var s persistedState
	if err := json.Unmarshal(data, &s); err != nil {
		return persistedState{}, err
	}
	return s, nil
}

// saveState writes atomically (tmp + rename) at mode 0600, same
// pattern as config.Save. Idempotent.
func saveState(s persistedState) error {
	dir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path, err := statePath()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
