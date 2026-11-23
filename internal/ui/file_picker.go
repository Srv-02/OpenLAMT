package ui

import (
	"path/filepath"

	"fyne.io/fyne/v2"
)

// The file dialogs of Export Config and Import Config. Both call chosen on the Fyne thread with
// the path of the file, and not at all when the user cancels. They are variables so tests
// choose a file without a dialog. macOS and Windows use the dialog of the operating system
// (file_picker_native.go), Linux and the others the Fyne dialog (file_picker_other.go), like
// the pickers of the media panel.
var (
	// pickSaveFile asks where to save: title of the dialog, suggested file name.
	pickSaveFile = func(parent fyne.Window, title, name string, chosen func(path string)) {
		saveFileDialog(parent, title, name, chosen)
	}
	// pickOpenFile asks which file to open.
	pickOpenFile = func(parent fyne.Window, title string, chosen func(path string)) {
		openFileDialog(parent, title, chosen)
	}
)

// showInFolder opens the folder that holds path in the file manager.
func showInFolder(path string) {
	openURL(fileURL(filepath.Dir(path)))
}
