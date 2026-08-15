package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

func prepareOpenWebUI(_, _ string) (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve Open WebUI data directory: %w", err)
	}
	return map[string]string{"DATA_DIR": filepath.Join(home, ".open-webui")}, nil
}
