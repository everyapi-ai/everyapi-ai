//go:build darwin

package computeruse

import (
	"path/filepath"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

func newDefaultService() (*Service, error) {
	configDir, err := config.ConfigDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(configDir, "computer-use")
	provider, err := newPlatformProvider(root)
	if err != nil {
		return nil, err
	}
	return NewService(provider, NewFileStore(filepath.Join(root, "state")), time.Now), nil
}
