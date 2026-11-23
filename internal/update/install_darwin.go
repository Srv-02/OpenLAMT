package update

import "os/exec"

// openFile opens path with open: a DMG is mounted and shown in the Finder.
func openFile(path string) error {
	return runOpener(exec.Command("open", path), openerWait)
}
