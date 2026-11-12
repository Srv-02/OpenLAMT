//go:build windows

package redir

import (
	"time"

	"golang.org/x/sys/windows"
)

// setSockUserTimeout drops the connection when retransmits stay unacknowledged for d
// (TCP_MAXRT, seconds).
func setSockUserTimeout(fd uintptr, d time.Duration) error {
	return windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_TCP, windows.TCP_MAXRT, int(d/time.Second))
}

func getSockUserTimeout(fd uintptr) (time.Duration, error) {
	v, err := windows.GetsockoptInt(windows.Handle(fd), windows.IPPROTO_TCP, windows.TCP_MAXRT)
	return time.Duration(v) * time.Second, err
}
