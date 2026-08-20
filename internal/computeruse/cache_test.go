package computeruse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreRoundTripUsesPrivatePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "computer-state")
	store := NewFileStore(root)
	want := SnapshotRecord{PID: 42, WindowID: 7, WindowFingerprint: "window", CreatedAt: time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC), Elements: []CachedElement{{Index: 12, Path: []int{1, 3}, Role: "AXButton", Fingerprint: "element"}}}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load(context.Background(), 42, 7)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.WindowFingerprint != want.WindowFingerprint || len(got.Elements) != 1 || got.Elements[0].Fingerprint != "element" {
		t.Fatalf("record = %+v, want %+v", got, want)
	}
	dirInfo, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat root: %v", err)
	}
	if gotMode := dirInfo.Mode().Perm(); gotMode != 0o700 {
		t.Fatalf("root mode = %o, want 700", gotMode)
	}
	fileInfo, err := os.Stat(filepath.Join(root, "42-7.json"))
	if err != nil {
		t.Fatalf("stat record: %v", err)
	}
	if gotMode := fileInfo.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("record mode = %o, want 600", gotMode)
	}
}

func TestFileStoreMissingRecordUsesSentinel(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "computer-state"))
	_, err := store.Load(context.Background(), 42, 7)
	if !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Load error = %v, want ErrSnapshotNotFound", err)
	}
}

func TestFileStoreDeleteIsIdempotent(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "computer-state"))
	record := SnapshotRecord{PID: 42, BundleID: "com.apple.TextEdit", WindowID: 7, CreatedAt: time.Now()}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete(context.Background(), 42, 7); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(context.Background(), 42, 7); err != nil {
		t.Fatalf("idempotent Delete: %v", err)
	}
	if _, err := store.Load(context.Background(), 42, 7); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Load after Delete = %v", err)
	}
}

func TestFileStoreSaveRemovesExpiredSnapshots(t *testing.T) {
	now := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	store := NewFileStore(filepath.Join(t.TempDir(), "computer-state"))
	store.now = func() time.Time { return now }
	old := SnapshotRecord{PID: 41, WindowID: 6, CreatedAt: now.Add(-snapshotTTL - time.Second)}
	if err := store.Save(context.Background(), old); err != nil {
		t.Fatalf("Save old snapshot: %v", err)
	}
	current := SnapshotRecord{PID: 42, WindowID: 7, CreatedAt: now}
	if err := store.Save(context.Background(), current); err != nil {
		t.Fatalf("Save current snapshot: %v", err)
	}
	if _, err := store.Load(context.Background(), old.PID, old.WindowID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("old Load error = %v, want ErrSnapshotNotFound", err)
	}
	if _, err := store.Load(context.Background(), current.PID, current.WindowID); err != nil {
		t.Fatalf("current Load: %v", err)
	}
}
