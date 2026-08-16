package edge

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// saveNodeMeta overwrites node.json with the given meta, preserving 0600 perms. Used by `start` to record the resolved mode and by future `edge update --gateway URL` to migrate gateway URL after a backend domain move.
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
	return atomicWriteNodeJSON(filepath.Join(dir, "node.json"), b)
}

// atomicWriteNodeJSON writes node.json via a temp-file-then-rename so an interrupted or partial write can never leave a truncated node.json — the file holds the one-time registration token (register.go) and the resolved mode (start), and there is no other copy of the token. The temp file is created 0600 in the same directory (same filesystem, so os.Rename is atomic); on any failure it is cleaned up and the original file is left untouched.
func atomicWriteNodeJSON(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".node-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
