//go:build !darwin && !windows

package ui

// nativeFilePickers: Linux and the other systems use the Fyne dialogs (fyneSaveDialog,
// fyneFolderDialog).
func nativeFilePickers() (filePickers, bool) { return filePickers{}, false }
