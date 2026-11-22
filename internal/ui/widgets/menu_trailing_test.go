package widgets

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// A trailing symbol sits at the right end of its row, 14 px, and widens the row; other rows
// are unchanged. PanelSize is the panel's size.
func TestMenuPopupTrailingIcon(t *testing.T) {
	themedApp(t)
	plain := fyne.NewMenuItem("Alpha", nil)
	ext := fyne.NewMenuItem("Gamma", nil)
	w := test.NewTempWindow(t, canvas.NewRectangle(color.Black))
	w.Resize(fyne.NewSize(500, 300))
	p := NewMenuPopup(fyne.NewMenu("", plain, ext), w.Canvas())
	row0, row1 := p.rows[0], p.rows[1]
	base := row0.MinSize().Width
	before := row1.MinSize().Width
	p.SetTrailing(ext, icons.ExternalLink)
	if got := row1.MinSize().Width - before; got != 16+14 {
		t.Errorf("trailing symbol widens the row by %v, want 30", got)
	}
	if row0.MinSize().Width != base {
		t.Error("a row without trailing symbol changed")
	}
	p.ShowAtPosition(fyne.NewPos(20, 30))
	rr := test.WidgetRenderer(row1).(*menuRowRenderer)
	if !rr.trailing.Visible() || rr.trailing.Size() != fyne.NewSquareSize(14) {
		t.Fatalf("trailing visible %v size %v", rr.trailing.Visible(), rr.trailing.Size())
	}
	if right := rr.trailing.Position().X + 14; right != row1.Size().Width-menuRowPadH {
		t.Errorf("symbol ends at %v, want %v", right, row1.Size().Width-menuRowPadH)
	}
	if rr.trailing.Resource() != icons.Get(icons.ExternalLink, icons.Secondary) {
		t.Error("idle symbol is not the secondary variant")
	}
	p.move(1)
	p.move(1)
	if rr.trailing.Resource() != icons.Get(icons.ExternalLink, icons.White) {
		t.Error("highlighted symbol is not white")
	}
	if rr0 := test.WidgetRenderer(row0).(*menuRowRenderer); rr0.trailing.Visible() {
		t.Error("row without trailing symbol shows one")
	}
	ext.Disabled = true
	p.Refresh()
	if rr.trailing.Resource() != icons.Get(icons.ExternalLink, icons.Disabled) {
		t.Error("disabled symbol is not the disabled variant")
	}
	if ps := p.PanelSize(); ps.Width < menuWidth || ps.Height <= 0 {
		t.Errorf("panel size %v", ps)
	}
}
