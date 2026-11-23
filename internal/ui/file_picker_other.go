//go:build !darwin && !windows

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
)

// saveFileDialog is the Fyne dialog. It creates the file when the user confirms; the caller
// writes it again through a temporary file, so only the path is taken from it.
func saveFileDialog(parent fyne.Window, title, name string, chosen func(path string)) {
	dlg := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			showError(parent, err)
			return
		}
		if w == nil {
			return
		}
		path := w.URI().Path()
		w.Close()
		chosen(path)
	}, parent)
	dlg.SetFileName(name)
	dlg.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	dlg.Resize(mediaDialogSize)
	dlg.Show()
}

func openFileDialog(parent fyne.Window, title string, chosen func(path string)) {
	dlg := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			showError(parent, err)
			return
		}
		if r == nil {
			return
		}
		path := r.URI().Path()
		r.Close()
		chosen(path)
	}, parent)
	dlg.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	dlg.Resize(mediaDialogSize)
	dlg.Show()
}
