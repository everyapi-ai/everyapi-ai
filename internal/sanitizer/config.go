package sanitizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// FileConfig is the on-disk shape of ~/.config/everyapi/sanitizer.json.
// JSON (not TOML as the spec text loosely mentions) is used here for
// consistency with credentials.json — keeping the CLI zero-dependency
// is worth more than the format flag waving. The file is mode 0600 so
// the disabled-rules list and any custom regex patterns aren't
// world-readable.
//
// Default behaviour when the file doesn't exist:
//   - Every built-in detector is active
//   - No custom patterns
//
// A user-config file therefore only needs to specify the *deviations*
// from default: which detectors to disable, plus any custom patterns
// to add.
type FileConfig struct {
	// Disabled lists built-in detector names to switch off. Entries
	// that don't match a known detector name are ignored at load
	// time — the file is forward-compatible if a future binary
	// drops a detector.
	Disabled []string `json:"disabled,omitempty"`

	// CustomPatterns is the user-defined regex set. Each entry
	// becomes a Detector with a `custom_<name>` registered name.
	// Invalid regexes are dropped at load time (CompileUserPatterns
	// already does the right thing).
	CustomPatterns []UserPattern `json:"custom_patterns,omitempty"`
}

// ConfigPath resolves to ~/.config/everyapi/sanitizer.json. Returns the
// error from the underlying config helper unchanged.
func ConfigPath() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sanitizer.json"), nil
}

// LoadFileConfig reads ConfigPath() if it exists. Missing file →
// returns an empty FileConfig + nil error (use defaults). Other I/O
// or parse errors bubble up.
func LoadFileConfig() (*FileConfig, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &FileConfig{}, nil
		}
		return nil, fmt.Errorf("read sanitizer config: %w", err)
	}
	var fc FileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parse sanitizer config %s: %w", path, err)
	}
	return &fc, nil
}

// SaveFileConfig writes fc to ConfigPath() with 0600 mode. Creates
// the parent directory if needed. The write is atomic via a temp
// file + rename so a partial write can't leave the user with a
// half-truncated config.
func SaveFileConfig(fc *FileConfig) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	body, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sanitizer config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("write sanitizer config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename sanitizer config: %w", err)
	}
	return nil
}

// BuildDetectors returns the active detector set described by fc:
// every built-in NOT in fc.Disabled, plus every valid custom pattern.
// Used by the server bootstrap so a single helper threads from
// "config on disk" to "detector slice the engine takes".
//
// Passing a nil receiver is OK — returns BuiltinDetectors() unchanged.
func (fc *FileConfig) BuildDetectors() []Detector {
	disabled := make(map[string]bool)
	if fc != nil {
		for _, name := range fc.Disabled {
			disabled[name] = true
		}
	}
	var out []Detector
	for _, d := range BuiltinDetectors() {
		if disabled[d.Name()] {
			continue
		}
		out = append(out, d)
	}
	if fc != nil && len(fc.CustomPatterns) > 0 {
		out = append(out, CompileUserPatterns(fc.CustomPatterns)...)
	}
	return out
}

// AllBuiltinNames returns the names of every shipped detector. Used
// by the configure wizard to render the toggle list.
func AllBuiltinNames() []string {
	bs := BuiltinDetectors()
	names := make([]string, 0, len(bs))
	for _, d := range bs {
		names = append(names, d.Name())
	}
	return names
}
