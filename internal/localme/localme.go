// Package localme detects whether this computer has an Intel ME device (#11). It only looks
// for the device, without administrator rights: it never opens the device and never sends
// anything to it. Reading Intel AMT through the device comes later.
package localme

import (
	"os"
	"runtime"
)

// Detector reports whether an Intel ME device is present.
type Detector func() bool

// Present reports whether this computer has an Intel ME device: on Linux the device node of
// the mei driver, on Windows the Intel MEI device interface. Never on macOS.
func Present() bool { return detect(runtime.GOOS, platformPresent) }

// Fake returns a Detector with a fixed answer, for tests.
func Fake(found bool) Detector { return func() bool { return found } }

// detect runs probe unless goos has no Intel ME (macOS: Intel Macs have an ME, but macOS
// ships no driver for it).
func detect(goos string, probe Detector) bool {
	if goos == "darwin" || probe == nil {
		return false
	}
	return probe()
}

// anyExists reports whether one of paths exists. Stat does not open the device.
func anyExists(paths ...string) bool {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
