//go:build darwin

package redir

import (
	"time"

	"golang.org/x/sys/unix"
)

// setSockUserTimeout drops the connection when retransmits stay unacknowledged for d
// (TCP_RXT_CONNDROPTIME, seconds).
func setSockUserTimeout(fd uintptr, d time.Duration) error {
	return unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_RXT_CONNDROPTIME, int(d/time.Second))
}

func getSockUserTimeout(fd uintptr) (time.Duration, error) {
	v, err := unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_RXT_CONNDROPTIME)
	return time.Duration(v) * time.Second, err
}
