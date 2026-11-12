package widgets

import (
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

// The info line sits above the items: name at the left, detail at the right, not selectable
// with the keyboard; the detail can change while the menu is open.
func TestMenuPopupInfoLine(t *testing.T) {
	themedApp(t)
	var ran []string
	a := fyne.NewMenuItem("A", func() { ran = append(ran, "A") })
	b := fyne.NewMenuItem("B", func() { ran = append(ran, "B") })
	m := fyne.NewMenu("", fyne.NewMenuItemSeparator(), a, fyne.NewMenuItemSeparator(), b)
	w := test.NewTempWindow(t, canvas.NewRectangle(color.Black))
	w.Resize(fyne.NewSize(400, 300))
	info := NewMenuInfo("alpine-standard.iso", "18.2 MB/s")
	p := NewMenuPopup(fyne.NewMenu(""), w.Canvas())
	p.SetMenu(m, info)
	p.ShowAtPosition(fyne.NewPos(20, 30))

	if info.Position().Y != 0 || info.Size().Width != menuWidth-2*menuPad {
		t.Fatalf("info at %v size %v", info.Position(), info.Size())
	}
	ih := info.MinSize().Height
	if want := 2*menuPad + ih + 2*9 + 2*menuRowHeight + 4*menuRowGap; p.panel.Size().Height != want {
		t.Fatalf("height %v, want %v", p.panel.Size().Height, want)
	}
	r := test.WidgetRenderer(info).(*menuInfoRenderer)
	if r.name.Text != "alpine-standard.iso" || !r.name.TextStyle.Bold || r.detail.Text != "18.2 MB/s" || r.detail.TextStyle.Bold {
		t.Fatalf("texts %q %q", r.name.Text, r.detail.Text)
	}
	if r.name.Position().X != menuInfoPadH || r.name.Position().Y != menuInfoPadTop {
		t.Fatalf("name at %v", r.name.Position())
	}
	if right := r.detail.Position().X + r.detail.MinSize().Width; right != info.Size().Width-menuInfoPadH {
		t.Fatalf("detail ends at %v, width %v", right, info.Size().Width)
	}
	if r.track.Visible() || r.fill.Visible() {
		t.Fatal("progress bar without progress")
	}

	// The keyboard starts at the first item; the info line is no item.
	p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if p.active != 1 {
		t.Fatalf("active %d", p.active)
	}
	test.TapAt(p, info.Position().Add(p.panel.Position()).Add(fyne.NewPos(menuPad+20, menuPad+5)))
	if !p.Visible() || len(ran) != 0 {
		t.Fatal("a tap on the info line closed the menu or ran an item")
	}

	info.SetDetail("0.4 MB/s")
	if r.detail.Text != "0.4 MB/s" || info.Detail() != "0.4 MB/s" {
		t.Fatalf("detail %q", r.detail.Text)
	}
	// A long name ends in "…" before the detail.
	info.SetName(strings.Repeat("very-long-image-name-", 6) + ".iso")
	if !strings.HasSuffix(r.name.Text, "…") || r.name.Position().X+r.name.MinSize().Width > r.detail.Position().X-menuInfoGap+0.5 {
		t.Fatalf("name %q at %v, detail at %v", r.name.Text, r.name.Position(), r.detail.Position())
	}
	test.Tap(p.Item(1))
	if len(ran) != 1 || ran[0] != "A" {
		t.Fatalf("ran %v", ran)
	}
}

// SetProgress shows a 4 px bar below the texts; its fill follows the value.
func TestMenuInfoProgress(t *testing.T) {
	themedApp(t)
	info := NewMenuInfo("firmware-tools", "")
	w := test.NewTempWindow(t, info)
	before := info.MinSize().Height
	info.SetProgress(0.62)
	w.Resize(fyne.NewSize(270, 100))
	info.Resize(fyne.NewSize(260, info.MinSize().Height))
	r := test.WidgetRenderer(info).(*menuInfoRenderer)
	if got := info.MinSize().Height; got != before+menuBarMarginTop+menuBarHeight+menuBarMarginBottom-menuInfoPadBottom {
		t.Fatalf("height %v, before %v", got, before)
	}
	if !r.track.Visible() || !r.fill.Visible() || r.track.Size().Height != menuBarHeight || r.track.Size().Width != 260-2*menuInfoPadH {
		t.Fatalf("track %v", r.track.Size())
	}
	if want := 0.62 * (260 - 2*menuInfoPadH); r.fill.Size().Width < want-0.01 || r.fill.Size().Width > want+0.01 || r.fill.Position() != r.track.Position() {
		t.Fatalf("fill %v at %v, track at %v", r.fill.Size(), r.fill.Position(), r.track.Position())
	}
	info.SetProgress(2)
	if v, on := info.Progress(); v != 1 || !on {
		t.Fatalf("progress %v %v", v, on)
	}
}

// SetMenu replaces the items of an open menu without closing it.
func TestMenuPopupSetMenuWhileShown(t *testing.T) {
	themedApp(t)
	var ran []string
	w := test.NewTempWindow(t, canvas.NewRectangle(color.Black))
	w.Resize(fyne.NewSize(400, 300))
	p := NewMenuPopup(fyne.NewMenu("", fyne.NewMenuItem("Old", func() { ran = append(ran, "Old") })), w.Canvas())
	dismissed := 0
	p.OnDismiss = func() { dismissed++ }
	p.ShowAtPosition(fyne.NewPos(20, 30))
	p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	info := NewMenuInfo("x", "")
	p.SetMenu(fyne.NewMenu("", fyne.NewMenuItemSeparator(), fyne.NewMenuItem("New", func() { ran = append(ran, "New") })), info)
	if !p.Visible() || dismissed != 0 || p.active != -1 || len(p.rows) != 2 {
		t.Fatalf("visible %v dismissed %d active %d rows %d", p.Visible(), dismissed, p.active, len(p.rows))
	}
	if want := 2*menuPad + info.MinSize().Height + 9 + menuRowHeight + 2*menuRowGap; p.panel.Size().Height != want {
		t.Fatalf("height %v, want %v", p.panel.Size().Height, want)
	}
	test.Tap(p.Item(1))
	if len(ran) != 1 || ran[0] != "New" || dismissed != 1 {
		t.Fatalf("ran %v dismissed %d", ran, dismissed)
	}
}

// Dismissing a menu keeps an overlay that was shown above it (a question asked while the menu
// was open); Fyne's OverlayStack.Remove would drop it.
func TestMenuPopupDismissKeepsOverlaysAbove(t *testing.T) {
	themedApp(t)
	w := test.NewTempWindow(t, canvas.NewRectangle(color.Black))
	w.Resize(fyne.NewSize(400, 300))
	c := w.Canvas()
	p := NewMenuPopup(fyne.NewMenu("", fyne.NewMenuItem("A", nil)), c)
	p.ShowAtPosition(fyne.NewPos(20, 30))
	above := NewMenuPopup(fyne.NewMenu("", fyne.NewMenuItem("B", nil)), c)
	above.ShowAtPosition(fyne.NewPos(40, 40))
	p.Dismiss()
	if l := c.Overlays().List(); len(l) != 1 || l[0] != above || c.Focused() != above {
		t.Fatalf("overlays %v focused %v", l, c.Focused())
	}
}
