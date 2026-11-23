//go:build linux

package localme

// linuxPaths are the device node and the sysfs entry of the first device of the mei driver.
var linuxPaths = []string{"/dev/mei0", "/sys/class/mei/mei0"}

func platformPresent() bool { return anyExists(linuxPaths...) }
