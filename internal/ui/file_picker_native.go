//go:build darwin || windows

package ui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"github.com/ncruces/zenity"
)

// jsonFilter limits the dialogs to JSON files.
func jsonFilter() zenity.Option {
	return zenity.FileFilters{{Name: lang.L("JSON files"), Patterns: []string{"*.json"}, CaseFold: true}}
}

// native runs a zenity dialog on its own goroutine; chosen runs on the Fyne thread.
func native(parent fyne.Window, chosen func(path string), dialog func() (string, error)) {
	go func() {
		path, err := dialog()
		fyne.Do(func() {
			switch {
			case errors.Is(err, zenity.ErrCanceled):
			case err != nil:
				showError(parent, err)
			case path != "":
				chosen(path)
			}
		})
	}()
}

func saveFileDialog(parent fyne.Window, title, name string, chosen func(path string)) {
	native(parent, chosen, func() (string, error) {
		return zenity.SelectFileSave(zenity.Title(title), zenity.Filename(name), zenity.ConfirmOverwrite(), jsonFilter())
	})
}

func openFileDialog(parent fyne.Window, title string, chosen func(path string)) {
	native(parent, chosen, func() (string, error) {
		return zenity.SelectFile(zenity.Title(title), jsonFilter())
	})
}
