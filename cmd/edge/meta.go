package edge

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// saveNodeMeta overwrites node.json with the given meta, preserving
// 0600 perms. Used by `start` to record the resolved mode and by
// future `edge update --gateway URL` to migrate gateway URL after a
// backend domain move.
func saveNodeMeta(id int, meta *nodeMeta) error {
	dir, err := nodeDir(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "node.json"), b, 0o600)
}
