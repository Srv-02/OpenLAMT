//go:build !linux && !windows

package localme

// newReader has no Intel ME to open: macOS ships no driver for it.
func newReader() reader { return nil }

// newChanger has no Intel ME to change.
func newChanger() changer { return nil }
