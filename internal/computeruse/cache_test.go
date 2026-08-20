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
	got, err := store.Load(context.Background(), "", 42, 7)
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
	_, err := store.Load(context.Background(), "", 42, 7)
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
	if err := store.Delete(context.Background(), "", 42, 7); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(context.Background(), "", 42, 7); err != nil {
		t.Fatalf("idempotent Delete: %v", err)
	}
	if _, err := store.Load(context.Background(), "", 42, 7); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("Load after Delete = %v", err)
	}
}

func TestFileStoreNamespacesRecordsBySession(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "computer-state"))
	first := SnapshotRecord{SessionID: "agent-a", PID: 42, BundleID: "com.apple.TextEdit", WindowID: 7, WindowFingerprint: "from-agent-a", CreatedAt: time.Now()}
	second := SnapshotRecord{SessionID: "agent-b", PID: 42, BundleID: "com.apple.TextEdit", WindowID: 7, WindowFingerprint: "from-agent-b", CreatedAt: time.Now()}
	if err := store.Save(context.Background(), first); err != nil {
		t.Fatalf("Save agent-a: %v", err)
	}
	if err := store.Save(context.Background(), second); err != nil {
		t.Fatalf("Save agent-b: %v", err)
	}
	gotFirst, err := store.Load(context.Background(), "agent-a", 42, 7)
	if err != nil {
		t.Fatalf("Load agent-a: %v", err)
	}
	if gotFirst.WindowFingerprint != "from-agent-a" {
		t.Fatalf("agent-a record = %+v, want fingerprint from-agent-a", gotFirst)
	}
	gotSecond, err := store.Load(context.Background(), "agent-b", 42, 7)
	if err != nil {
		t.Fatalf("Load agent-b: %v", err)
	}
	if gotSecond.WindowFingerprint != "from-agent-b" {
		t.Fatalf("agent-b record = %+v, want fingerprint from-agent-b", gotSecond)
	}
	if err := store.Delete(context.Background(), "agent-a", 42, 7); err != nil {
		t.Fatalf("Delete agent-a: %v", err)
	}
	if _, err := store.Load(context.Background(), "agent-a", 42, 7); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("agent-a Load after its own Delete = %v, want ErrSnapshotNotFound", err)
	}
	if _, err := store.Load(context.Background(), "agent-b", 42, 7); err != nil {
		t.Fatalf("agent-b Load after agent-a's Delete: %v, want agent-b's record untouched", err)
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
	if _, err := store.Load(context.Background(), "", old.PID, old.WindowID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("old Load error = %v, want ErrSnapshotNotFound", err)
	}
	if _, err := store.Load(context.Background(), "", current.PID, current.WindowID); err != nil {
		t.Fatalf("current Load: %v", err)
	}
}
