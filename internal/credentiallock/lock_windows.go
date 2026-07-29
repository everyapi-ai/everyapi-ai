//go:build windows

package credentiallock

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/config"
	"golang.org/x/sys/windows"
)

func Acquire() (func(), error) {
	return acquire(0)
}

func AcquireTimeout(timeout time.Duration) (func(), error) {
	return acquire(timeout)
}

func acquire(timeout time.Duration) (func(), error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "credentials.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if timeout > 0 {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	deadline := time.Now().Add(timeout)
	for {
		err = windows.LockFileEx(h, flags, 0, 1, 0, ol)
		if err == nil {
			break
		}
		if timeout <= 0 {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, os.ErrDeadlineExceeded
		}
		time.Sleep(50 * time.Millisecond)
	}
	var once sync.Once
	return func() { once.Do(func() { _ = windows.UnlockFileEx(h, 0, 1, 0, ol); _ = f.Close() }) }, nil
}
