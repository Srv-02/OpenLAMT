// Package localme is "This computer" (#11). Present detects, without administrator rights,
// whether this computer has an Intel ME device: it only looks for the device, never opens it and
// never sends anything to it. Check reads Intel AMT through the device in a short elevated
// helper process (check.go); the setup actions turn Intel AMT on, activate it in Client Control
// Mode or deactivate it the same way (setup.go).
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
