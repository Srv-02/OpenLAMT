//go:build !windows

package update

import "errors"

// elevateReplace exists only on Windows; the update replaces no program elsewhere.
func elevateReplace(newExe, exe, sum string) error {
	return errors.New("update: elevation is only used on Windows")
}

// RemoveLeftover deletes the exe a Windows update replaced; elsewhere there is none.
func RemoveLeftover() error { return nil }
