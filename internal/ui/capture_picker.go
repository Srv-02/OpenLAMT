package ui

import (
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/storage"
)

// Save and folder dialogs of the KVM capture (issues #4 and #26), chosen as for the Media menu
// (media_picker_native.go): the dialogs of the operating system through zenity on macOS and
// Windows, Fyne's dialogs on Linux. chosen and failed run on the Fyne thread; a cancelled
// dialog calls neither.

// captureKind is what a file holds: its extension, the dialog title and the file filter name.
type captureKind struct{ ext, title, filter string }

func screenshotKind() captureKind {
	return captureKind{ext: "png", title: lang.L("Save Screenshot"), filter: lang.L("PNG images")}
}

func recordingKind() captureKind {
	return captureKind{ext: "avi", title: lang.L("Save Recording"), filter: lang.L("AVI videos")}
}

// filePickers are the dialogs: save proposes name for a file of kind, folder chooses a folder.
type filePickers struct {
	save   func(win fyne.Window, kind captureKind, name string, chosen func(path string), failed func(error))
	folder func(win fyne.Window, chosen func(path string), failed func(error))
}

// defaultPickers returns the dialogs of this platform.
func defaultPickers() filePickers {
	if p, ok := nativeFilePickers(); ok {
		return p
	}
	return filePickers{save: fyneSaveDialog, folder: fyneFolderDialog}
}

// withExt adds .ext to path unless it already ends in it (any case): a name typed without the
// extension still opens in the right program.
func withExt(path, ext string) string {
	if strings.EqualFold(filepath.Ext(path), "."+ext) {
		return path
	}
	return path + "." + ext
}

// fyneSaveDialog is Fyne's save dialog with the proposed name; it asks before it overwrites.
// The dialog creates the file; the caller writes it again under the same path.
func fyneSaveDialog(win fyne.Window, kind captureKind, name string, chosen func(path string), failed func(error)) {
	dlg := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			failed(err)
			return
		}
		if w == nil {
			return
		}
		path := w.URI().Path()
		w.Close()
		chosen(withExt(path, kind.ext))
	}, win)
	dlg.SetFileName(name)
	dlg.SetFilter(storage.NewExtensionFileFilter([]string{"." + kind.ext}))
	dlg.Resize(mediaDialogSize)
	dlg.Show()
}

// fyneFolderDialog is Fyne's folder dialog.
func fyneFolderDialog(win fyne.Window, chosen func(path string), failed func(error)) {
	dlg := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil {
			failed(err)
			return
		}
		if u != nil {
			chosen(u.Path())
		}
	}, win)
	dlg.Resize(mediaDialogSize)
	dlg.Show()
}
