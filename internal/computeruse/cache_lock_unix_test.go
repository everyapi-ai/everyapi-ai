//go:build darwin || linux

package computeruse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreOperationLockSerializesInstancesAndHonorsCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "computer-state")
	first := NewFileStore(root)
	second := NewFileStore(root)
	unlock, err := first.Lock(context.Background())
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := second.Lock(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("contended Lock error = %v, want context.Canceled", err)
	}
	unlock()
	secondUnlock, err := second.Lock(context.Background())
	if err != nil {
		t.Fatalf("second Lock after release: %v", err)
	}
	secondUnlock()
}

func TestOperationLockOldUnlockCannotRemoveSuccessor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "computer-state")
	store := NewFileStore(root)
	firstUnlock, err := store.Lock(context.Background())
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	lockPath := filepath.Join(root, ".operation.lock")
	retiredPath := filepath.Join(root, ".operation.lock.retired")
	if err := os.Rename(lockPath, retiredPath); err != nil {
		t.Fatalf("retire first lock: %v", err)
	}
	secondUnlock, err := store.Lock(context.Background())
	if err != nil {
		t.Fatalf("second Lock: %v", err)
	}
	firstUnlock()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("first unlock removed successor lock: %v", err)
	}
	secondUnlock()
	_ = os.Remove(retiredPath)
}

func TestOperationLockRecoversDeadOwner(t *testing.T) {
	root := filepath.Join(t.TempDir(), "computer-state")
	lockPath := filepath.Join(root, ".operation.lock")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("999999:dead-owner"), 0o600); err != nil {
		t.Fatalf("write stale owner: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := NewFileStore(root).Lock(ctx)
	if err != nil {
		t.Fatalf("recover dead owner: %v", err)
	}
	unlock()
}

func TestOperationLockDeadArtifactHasSingleConcurrentWinner(t *testing.T) {
	root := filepath.Join(t.TempDir(), "computer-state")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".operation.lock"), []byte("dead owner artifact"), 0o600); err != nil {
		t.Fatalf("create dead lock artifact: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	type lockResult struct {
		unlock func()
		err    error
	}
	start := make(chan struct{})
	results := make(chan lockResult, 2)
	for range 2 {
		go func() {
			<-start
			unlock, err := NewFileStore(root).Lock(ctx)
			results <- lockResult{unlock: unlock, err: err}
		}()
	}
	close(start)
	first := <-results
	if first.err != nil {
		t.Fatalf("first concurrent lock: %v", first.err)
	}
	cancel()
	second := <-results
	if !errors.Is(second.err, context.Canceled) {
		if second.unlock != nil {
			second.unlock()
		}
		first.unlock()
		t.Fatalf("second concurrent lock error = %v, want context.Canceled", second.err)
	}
	first.unlock()
}
