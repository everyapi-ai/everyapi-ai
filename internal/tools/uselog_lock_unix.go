//go:build !windows

package tools

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockUseLog takes an advisory exclusive (LOCK_EX) lock on f for the use.log serialization in writeUseLogLine, and returns an unlock func. A blocking lock is deliberate: contending `everyapi use` processes each hold it only for the microseconds of one trim+append, and the whole call already runs under logToolExit's write-timeout goroutine, so a pathological holder can't stall the exit path. If the lock can't be taken the returned func is a no-op and the caller proceeds best-effort (an interleaved line beats a lost death line).
func lockUseLog(f *os.File) func() {
	fd := int(f.Fd())
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return func() {}
	}
	return func() { _ = unix.Flock(fd, unix.LOCK_UN) }
}
