//go:build !linux && !windows

package localme

// platformCheckLMS: there is no activation without an Intel ME.
func platformCheckLMS() error { return errNoME }
