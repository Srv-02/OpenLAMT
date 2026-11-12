//go:build !linux && !darwin && !windows

package redir

import "time"

// No TCP user timeout on this platform: a lost link is noticed only by the operating system.
func setSockUserTimeout(uintptr, time.Duration) error { return nil }

func getSockUserTimeout(uintptr) (time.Duration, error) { return 0, nil }
