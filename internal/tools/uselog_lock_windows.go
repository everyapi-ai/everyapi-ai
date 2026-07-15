//go:build windows

package tools

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockUseLog takes an exclusive byte-range lock over f for the use.log
// serialization in writeUseLogLine, and returns an unlock func. Windows
// has no flock; LockFileEx with LOCKFILE_EXCLUSIVE_LOCK over a fixed
// range gives the same mutual exclusion between `everyapi use`
// processes. Best-effort: if the lock can't be taken the returned func
// is a no-op and the caller proceeds unlocked.
func lockUseLog(f *os.File) func() {
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	// Lock a fixed, large range from offset 0 — every writer locks the
	// same range, so they serialize regardless of file size.
	const lockLo, lockHi = ^uint32(0), ^uint32(0)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, lockLo, lockHi, ol); err != nil {
		return func() {}
	}
	return func() { _ = windows.UnlockFileEx(h, 0, lockLo, lockHi, ol) }
}
