//go:build darwin || windows

package ui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"github.com/ncruces/zenity"
)

// nativeFilePickers returns the save and folder dialogs of the operating system (the Finder
// panel on macOS, the Explorer dialog on Windows) through zenity, as nativePickers of the Media
// menu. Each dialog blocks its own goroutine.
func nativeFilePickers() (filePickers, bool) {
	answer := func(path string, err error, chosen func(string), failed func(error)) {
		fyne.Do(func() {
			switch {
			case errors.Is(err, zenity.ErrCanceled):
			case err != nil:
				failed(err)
			case path != "":
				chosen(path)
			}
		})
	}
	save := func(win fyne.Window, kind captureKind, name string, chosen func(path string), failed func(error)) {
		go func() {
			path, err := zenity.SelectFileSave(zenity.Title(kind.title), zenity.Filename(name), zenity.ConfirmOverwrite(),
				zenity.FileFilters{{Name: kind.filter, Patterns: []string{"*." + kind.ext}, CaseFold: true}})
			// The panel of macOS asks about the typed name only: finishSave asks again if the
			// app adds the extension and that file exists (Windows adds it itself).
			answer(path, err, func(picked string) { finishSave(win, kind, picked, chosen) }, failed)
		}()
	}
	folder := func(_ fyne.Window, chosen func(path string), failed func(error)) {
		go func() {
			path, err := zenity.SelectFile(zenity.Title(lang.L("Screenshots and recordings")), zenity.Directory())
			answer(path, err, chosen, failed)
		}()
	}
	return filePickers{save: save, folder: folder}, true
}
