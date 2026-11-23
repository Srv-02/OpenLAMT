//go:build darwin || windows

package ui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"github.com/ncruces/zenity"
)

// openCertFile is the system file dialog (Finder panel, Explorer dialog) for a certificate file
// with one of exts. The dialog blocks its own goroutine; chosen runs on the Fyne thread.
func openCertFile(win fyne.Window, exts []string, chosen func(path string)) {
	patterns := make([]string, len(exts))
	for i, e := range exts {
		patterns[i] = "*" + e
	}
	go func() {
		path, err := zenity.SelectFile(zenity.Title(lang.L("Import certificate")),
			zenity.FileFilters{{Name: lang.L("Certificates"), Patterns: patterns, CaseFold: true}})
		fyne.Do(func() { pickDone(win, path, err, chosen) })
	}()
}

// saveCertFile is the system save dialog with name suggested; chosen writes the file at path.
func saveCertFile(win fyne.Window, name string, chosen func(path string)) {
	go func() {
		path, err := zenity.SelectFileSave(zenity.Title(lang.L("Save as File…")), zenity.Filename(name), zenity.ConfirmOverwrite(),
			zenity.FileFilters{{Name: "PEM", Patterns: []string{"*.pem"}, CaseFold: true}})
		fyne.Do(func() { pickDone(win, path, err, chosen) })
	}()
}

func pickDone(win fyne.Window, path string, err error, chosen func(string)) {
	switch {
	case errors.Is(err, zenity.ErrCanceled):
	case err != nil:
		showError(win, err)
	case path != "":
		chosen(path)
	}
}
