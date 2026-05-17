// Package config reads and writes ~/.config/relaya/credentials.json.
//
// We store the API base alongside the access token so a dev can point
// the CLI at a local backend without rebuilding (and so the same CLI
// binary works for self-hosters with a non-default base). Production
// users never edit this file — `relaya login` writes it.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultAPIBase is the production gateway. Hardcoded per
// docs/cli/channel-marketplace.md §7-3 (URL hardcode, no env override). The
// credentials file's api_base field overrides this at runtime for
// local development and self-hosters.
const DefaultAPIBase = "https://api.relaya.pro"

// Credentials is the on-disk credentials payload. JSON tags match the
// file format. Stored mode 0600.
type Credentials struct {
	APIBase     string `json:"api_base"`
	AccessToken string `json:"access_token"`
	UserID      int    `json:"user_id,omitempty"`
	Username    string `json:"username,omitempty"`
}

// ErrNoCredentials is returned by Load when the file is missing. The
// `relaya login` flow should produce a friendly "please log in"
// message on this; other errors (corrupt JSON, perms) bubble up as-is
// so the user sees the real problem.
var ErrNoCredentials = errors.New("not logged in")

// ConfigDir returns ~/.config/relaya (XDG_CONFIG_HOME respected on
// Linux; ~/.config is the de facto cross-platform location for CLI
// state — we deliberately don't use AppData/Library on Win/Mac to
// keep the path predictable across platforms for support).
func ConfigDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "relaya"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "relaya"), nil
}

func credentialsPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

// Load reads credentials from disk. Returns ErrNoCredentials when the
// file doesn't exist — callers should special-case that to print a
// "run 'relaya login' first" message rather than the raw error.
func Load() (*Credentials, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoCredentials
		}
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	if c.APIBase == "" {
		c.APIBase = DefaultAPIBase
	}
	return &c, nil
}

// Save writes credentials atomically (tmp + rename) at mode 0600. The
// atomic dance prevents a half-written file if the process is killed
// mid-write; mode 0600 keeps the token off prying eyes on shared
// machines (XDG_CONFIG_HOME is per-user already, but being explicit
// matches `gh auth` / `aws configure` conventions).
func Save(c *Credentials) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir config: %w", err)
	}
	path := filepath.Join(dir, "credentials.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename credentials: %w", err)
	}
	return nil
}

// Delete removes the credentials file. Returns nil on missing file
// (logout is idempotent — calling it twice shouldn't error).
func Delete() error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove credentials: %w", err)
	}
	return nil
}
