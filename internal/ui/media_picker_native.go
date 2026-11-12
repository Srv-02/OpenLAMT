//go:build darwin || windows

package ui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"github.com/ncruces/zenity"
)

// nativePickers returns the file and folder dialogs of the operating system (the Finder panel
// on macOS, the Explorer dialog on Windows) through zenity. The dialog blocks its own goroutine;
// chosen runs on the Fyne thread. Linux keeps the Fyne dialogs (nativePickers in
// media_picker_other.go).
func (m *mediaPanel) nativePickers() (iso, folder func(chosen func(path string)), ok bool) {
	pick := func(opts ...zenity.Option) func(chosen func(path string)) {
		return func(chosen func(path string)) {
			go func() {
				path, err := zenity.SelectFile(opts...)
				fyne.Do(func() {
					switch {
					case errors.Is(err, zenity.ErrCanceled):
					case err != nil:
						m.report(err)
					case path != "":
						chosen(path)
					}
				})
			}()
		}
	}
	iso = pick(zenity.Title(lang.L("Mount ISO…")),
		zenity.FileFilters{{Name: lang.L("ISO images"), Patterns: []string{"*.iso"}, CaseFold: true}})
	folder = pick(zenity.Title(lang.L("Mount Folder…")), zenity.Directory())
	return iso, folder, true
}
