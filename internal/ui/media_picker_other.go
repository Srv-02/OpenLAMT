//go:build !darwin && !windows

package ui

// nativePickers: Linux and the other systems use the Fyne dialogs (dialogISO, dialogFolder).
func (m *mediaPanel) nativePickers() (iso, folder func(chosen func(path string)), ok bool) {
	return nil, nil, false
}
