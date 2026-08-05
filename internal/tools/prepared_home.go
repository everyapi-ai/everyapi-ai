package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

const preparedHomeMarker = "__EVERYAPI_PREPARED_HOME"

// newPreparedHome creates a process-scoped client home. Live catalog and
// loopback proxy configuration must not be shared between concurrent launches
// using different relay keys or groups.
func newPreparedHome(prefix string) (string, error) {
	root, err := config.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve everyapi config dir: %w", err)
	}
	root = filepath.Join(root, "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create prepared client home root: %w", err)
	}
	home, err := os.MkdirTemp(root, prefix+"-")
	if err != nil {
		return "", fmt.Errorf("create prepared %s home: %w", prefix, err)
	}
	return home, nil
}

func preparedHomeEnv(key, home string) map[string]string {
	return map[string]string{key: home, preparedHomeMarker: home}
}

// TakePreparedCleanup removes the internal marker before the child receives
// its environment and returns an idempotent cleanup for the generated home.
func TakePreparedCleanup(env map[string]string) func() {
	home := env[preparedHomeMarker]
	delete(env, preparedHomeMarker)
	if home == "" {
		return nil
	}
	root, err := config.ConfigDir()
	if err != nil {
		return nil
	}
	root = filepath.Join(root, "sessions")
	rel, err := filepath.Rel(root, home)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil
	}
	var once sync.Once
	return func() {
		once.Do(func() { removePreparedHomeAfterQuiet(home) })
	}
}

func removePreparedHomeAfterQuiet(home string) {
	deadline := time.Now().Add(time.Second)
	var absentSince time.Time
	for {
		_ = os.RemoveAll(home)
		if _, err := os.Stat(home); os.IsNotExist(err) {
			if absentSince.IsZero() {
				absentSince = time.Now()
			} else if time.Since(absentSince) >= 100*time.Millisecond {
				return
			}
		} else {
			absentSince = time.Time{}
		}
		if time.Now().After(deadline) {
			_ = os.RemoveAll(home)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
