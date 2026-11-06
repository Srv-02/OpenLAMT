//go:build !linux

package ui

// waylandPlatform is only meaningful on Linux.
func waylandPlatform() bool { return false }
