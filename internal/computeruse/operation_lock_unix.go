//go:build darwin || linux

package computeruse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

func lockOperationFile(ctx context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open operation lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure operation lock: %w", err)
	}
	lockCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		lockCtx, cancel = context.WithTimeout(ctx, operationLockWait)
	}
	defer cancel()
	for {
		if err := lockCtx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			var once sync.Once
			return func() {
				once.Do(func() {
					_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
					_ = file.Close()
				})
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("acquire operation lock: %w", err)
		}
		timer := time.NewTimer(operationLockPoll)
		select {
		case <-lockCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, lockCtx.Err()
		case <-timer.C:
		}
	}
}
