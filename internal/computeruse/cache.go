package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	operationLockPoll = 25 * time.Millisecond
	operationLockWait = 30 * time.Second
)

type FileStore struct {
	root string
	now  func() time.Time
}

func NewFileStore(root string) *FileStore {
	return &FileStore{root: root, now: time.Now}
}

func (s *FileStore) path(sessionID string, pid, windowID int) string {
	return filepath.Join(s.root, snapshotKey(sessionID, pid, windowID)+".json")
}

func (s *FileStore) Lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create operation lock directory: %w", err)
	}
	if err := os.Chmod(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("secure operation lock directory: %w", err)
	}
	return lockOperationFile(ctx, filepath.Join(s.root, ".operation.lock"))
}

func (s *FileStore) Save(ctx context.Context, record SnapshotRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.PID <= 0 || record.WindowID <= 0 {
		return fmt.Errorf("snapshot identity must contain positive pid and window id")
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	if err := os.Chmod(s.root, 0o700); err != nil {
		return fmt.Errorf("secure snapshot directory: %w", err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	tmp, err := os.CreateTemp(s.root, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("create snapshot temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("secure snapshot temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write snapshot temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync snapshot temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close snapshot temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path(record.SessionID, record.PID, record.WindowID)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish snapshot: %w", err)
	}
	s.cleanupExpired(record.Key())
	return nil
}

func (s *FileStore) cleanupExpired(currentKey string) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.TrimSuffix(entry.Name(), ".json") == currentKey {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var record SnapshotRecord
		if json.Unmarshal(data, &record) != nil || record.CreatedAt.IsZero() {
			continue
		}
		if s.now().Sub(record.CreatedAt) > snapshotTTL {
			_ = os.Remove(path)
		}
	}
}

func (s *FileStore) Load(ctx context.Context, sessionID string, pid, windowID int) (SnapshotRecord, error) {
	if err := ctx.Err(); err != nil {
		return SnapshotRecord{}, err
	}
	data, err := os.ReadFile(s.path(sessionID, pid, windowID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SnapshotRecord{}, ErrSnapshotNotFound
		}
		return SnapshotRecord{}, fmt.Errorf("read snapshot: %w", err)
	}
	var record SnapshotRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return SnapshotRecord{}, fmt.Errorf("parse snapshot: %w", err)
	}
	if record.SessionID != sessionID || record.PID != pid || record.WindowID != windowID {
		return SnapshotRecord{}, fmt.Errorf("snapshot identity mismatch")
	}
	return record, nil
}

func (s *FileStore) Delete(ctx context.Context, sessionID string, pid, windowID int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(s.path(sessionID, pid, windowID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}
