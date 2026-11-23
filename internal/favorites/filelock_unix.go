//go:build unix

package favorites

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes an exclusive advisory lock (flock) on f, blocking until it is free.
func lockFile(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX)
		if err != unix.EINTR {
			return err
		}
	}
}

func unlockFile(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
