//go:build windows

package favorites

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock (LockFileEx) on f, blocking until it is free.
func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
}

func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
