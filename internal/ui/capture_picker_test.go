package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// A name typed without the extension: the empty file the dialog created is removed, and an
// existing file with the extension is replaced only after the question.
func TestFinishSaveAsksBeforeReplacingTheFileWithExtension(t *testing.T) {
	w, _ := panelHarness(t)
	dir := t.TempDir()
	typed, final := filepath.Join(dir, "report"), filepath.Join(dir, "report.png")
	if err := os.WriteFile(final, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(typed, nil, 0o600); err != nil { // what the Fyne dialog creates
		t.Fatal(err)
	}
	var got []string
	finishSave(w, screenshotKind(), typed, func(p string) { got = append(got, p) })
	if len(got) != 0 || !open(w) {
		t.Fatalf("no question before the file is replaced: chosen %v, open %v", got, open(w))
	}
	if _, err := os.Stat(typed); err == nil {
		t.Error("the empty file of the dialog is still there")
	}
	// Cancel keeps everything.
	overlayButton(t, w, "Cancel").OnTapped()
	if len(got) != 0 {
		t.Fatalf("chosen after Cancel: %v", got)
	}
	if b, _ := os.ReadFile(final); string(b) != "old" {
		t.Errorf("file changed: %q", b)
	}
	// Replace passes the file with the extension.
	finishSave(w, screenshotKind(), typed, func(p string) { got = append(got, p) })
	overlayButton(t, w, "Replace").OnTapped()
	if len(got) != 1 || got[0] != final {
		t.Fatalf("chosen %v, want %s", got, final)
	}
}

// Without an existing target nothing is asked; a name that already has the extension was
// asked about by the dialog and is kept as it is.
func TestFinishSaveWithoutConflict(t *testing.T) {
	w, _ := panelHarness(t)
	dir := t.TempDir()
	typed := filepath.Join(dir, "new")
	if err := os.WriteFile(typed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	finishSave(w, screenshotKind(), typed, func(p string) { got = append(got, p) })
	if len(got) != 1 || got[0] != typed+".png" || open(w) {
		t.Fatalf("chosen %v, open %v", got, open(w))
	}
	if _, err := os.Stat(typed); err == nil {
		t.Error("the empty file of the dialog is still there")
	}
	with := filepath.Join(dir, "x.PNG")
	os.WriteFile(with, nil, 0o600)
	got = nil
	finishSave(w, screenshotKind(), with, func(p string) { got = append(got, p) })
	if len(got) != 1 || got[0] != with || open(w) {
		t.Fatalf("chosen %v, open %v", got, open(w))
	}
	if _, err := os.Stat(with); err != nil {
		t.Error("the file with the extension was removed")
	}
}
