//go:build !linux && !windows

package localme

func platformPresent() bool { return false }
