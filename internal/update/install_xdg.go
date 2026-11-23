//go:build !windows && !darwin

package update

import "os/exec"

// openFile opens path with xdg-open: a .deb goes to the system's software installer, which
// asks for the authentication itself. Without an app for the file type xdg-open fails.
func openFile(path string) error {
	return runOpener(exec.Command("xdg-open", path), openerWait)
}
