//go:build linux

package redir

import (
	"time"

	"golang.org/x/sys/unix"
)

// setSockUserTimeout aborts the connection when sent data stays unacknowledged for d
// (TCP_USER_TIMEOUT, milliseconds).
func setSockUserTimeout(fd uintptr, d time.Duration) error {
	return unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(d.Milliseconds()))
}

func getSockUserTimeout(fd uintptr) (time.Duration, error) {
	v, err := unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT)
	return time.Duration(v) * time.Millisecond, err
}
