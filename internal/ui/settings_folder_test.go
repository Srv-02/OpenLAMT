package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// settingsButtons returns the buttons of the open settings dialog by text.
func settingsButtons(t *testing.T, w fyne.Window) map[string]*widgets.PrimaryButton {
	t.Helper()
	_, _, buttons := dialogTexts(w)
	return buttons
}

// TestSettingsFolderRow: the row "Screenshots and recordings" sits in "Remote screen", says
// "Not set: …" while empty, shows the chosen folder with "×" to clear it, and stores the
// value only on Save.
func TestSettingsFolderRow(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	dir := filepath.Join(t.TempDir(), "Captures")
	prev := pickCaptureFolder
	pickCaptureFolder = func(_ fyne.Window, chosen func(string), _ func(error)) { chosen(dir) }
	t.Cleanup(func() { pickCaptureFolder = prev })

	showSettingsDialog(s.win, &s.settings, s.checkUpdateNow)
	top := s.win.Canvas().Overlays().Top()
	if !hasText(top, "Screenshots and recordings") {
		t.Fatal("row missing")
	}
	var row *folderRow
	walk(top, func(o fyne.CanvasObject) {
		if e, ok := o.(*ellipsisText); ok && e.Text == "Not set: OpenLAMT asks for a file name each time." {
			row = &folderRow{desc: e}
		}
	})
	if row == nil {
		t.Fatal("no \"Not set\" text")
	}
	buttons := settingsButtons(t, s.win)
	choose, clear := buttons["Choose…"], buttons[""]
	if choose == nil || clear == nil || clear.Visible() || choose.Icon() == nil {
		t.Fatalf("buttons: choose %v clear %v", choose, clear)
	}
	test.Tap(choose)
	if row.desc.Text != dir || !row.desc.mono || !clear.Visible() {
		t.Fatalf("after choose: %q mono=%v clear=%v", row.desc.Text, row.desc.mono, clear.Visible())
	}
	if s.settings.CaptureFolder() != "" {
		t.Fatal("stored before Save")
	}
	test.Tap(buttons["Save"])
	if got := s.settings.CaptureFolder(); got != dir {
		t.Fatalf("stored %q", got)
	}

	showSettingsDialog(s.win, &s.settings, s.checkUpdateNow)
	buttons = settingsButtons(t, s.win)
	if !buttons[""].Visible() {
		t.Fatal("× hidden while a folder is set")
	}
	test.Tap(buttons[""])
	if buttons[""].Visible() {
		t.Fatal("× still shown after clearing")
	}
	test.Tap(buttons["Save"])
	if got := s.settings.CaptureFolder(); got != "" {
		t.Fatalf("not cleared: %q", got)
	}
}
