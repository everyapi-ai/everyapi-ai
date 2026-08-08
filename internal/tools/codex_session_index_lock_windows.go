//go:build windows

package tools

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockCodexSessionIndex(file *os.File) (func(), error) {
	handle := windows.Handle(file.Fd())
	overlapped := new(windows.Overlapped)
	const lockLo, lockHi = ^uint32(0), ^uint32(0)
	if err := windows.LockFileEx(
		handle,
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		lockLo,
		lockHi,
		overlapped,
	); err != nil {
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, lockLo, lockHi, overlapped)
	}, nil
}
