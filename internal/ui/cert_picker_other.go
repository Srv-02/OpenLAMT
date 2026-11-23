//go:build !darwin && !windows

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
)

// openCertFile is the Fyne file dialog for a certificate file with one of exts (Linux; macOS and
// Windows use the system dialog, cert_picker_native.go). chosen runs with the path.
func openCertFile(win fyne.Window, exts []string, chosen func(path string)) {
	dlg := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			showError(win, err)
			return
		}
		if r == nil {
			return
		}
		path := r.URI().Path()
		r.Close()
		chosen(path)
	}, win)
	dlg.SetFilter(storage.NewExtensionFileFilter(exts))
	dlg.Resize(mediaDialogSize)
	dlg.Show()
}

// saveCertFile is the Fyne save dialog with name suggested; chosen writes the file at path.
func saveCertFile(win fyne.Window, name string, chosen func(path string)) {
	dlg := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			showError(win, err)
			return
		}
		if w == nil {
			return
		}
		path := w.URI().Path()
		w.Close()
		chosen(path)
	}, win)
	dlg.SetFileName(name)
	dlg.SetFilter(storage.NewExtensionFileFilter([]string{".pem"}))
	dlg.Resize(mediaDialogSize)
	dlg.Show()
}
