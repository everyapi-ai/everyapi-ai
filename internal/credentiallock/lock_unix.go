//go:build !windows

package credentiallock

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/config"
	"golang.org/x/sys/unix"
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
	if timeout <= 0 {
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
			_ = f.Close()
			return nil, err
		}
	} else {
		deadline := time.Now().Add(timeout)
		for {
			err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
			if err == nil {
				break
			}
			if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
				_ = f.Close()
				return nil, err
			}
			if time.Now().After(deadline) {
				_ = f.Close()
				return nil, os.ErrDeadlineExceeded
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	var once sync.Once
	return func() { once.Do(func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }) }, nil
}
