package widgets

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

func same(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func TestSwitch(t *testing.T) {
	themedApp(t)
	var got []bool
	s := NewSwitch(func(on bool) { got = append(got, on) })
	w := test.NewTempWindow(t, s)
	r := test.WidgetRenderer(s).(*switchRenderer)

	if s.MinSize() != fyne.NewSize(38, 22) {
		t.Fatalf("min size %v", s.MinSize())
	}
	offX := r.knob.Position().X
	test.Tap(s)
	if !s.Checked() || len(got) != 1 || !got[0] {
		t.Fatalf("tap: checked=%v calls=%v", s.Checked(), got)
	}
	if w.Canvas().Focused() != s {
		t.Error("tap does not focus the switch")
	}
	if !same(r.track.FillColor, style.Color("primary")) || r.knob.Position().X-offX != style.SwitchWidth-style.SwitchHeight {
		t.Errorf("on: track %v, knob moved %v", r.track.FillColor, r.knob.Position().X-offX)
	}

	s.TypedRune(' ') // Space toggles, like widget.Check
	s.TypedRune('x')
	if s.Checked() || len(got) != 2 {
		t.Fatalf("space: checked=%v calls=%v", s.Checked(), got)
	}

	s.SetChecked(false) // unchanged: no callback
	if len(got) != 2 {
		t.Errorf("SetChecked without change called OnChanged")
	}

	s.Disable()
	test.Tap(s)
	s.TypedRune(' ')
	if s.Checked() || len(got) != 2 {
		t.Errorf("disabled switch toggled")
	}
}

func TestToolbarButton(t *testing.T) {
	themedApp(t)
	taps := 0
	b := NewToolbarButton(icons.KVM, "KVM", func() { taps++ })
	test.NewTempWindow(t, b)
	r := test.WidgetRenderer(b).(*toolbarButtonRenderer)

	if h := b.MinSize().Height; h != style.ToolbarItemHeight {
		t.Errorf("height %v", h)
	}
	test.Tap(b)
	b.TypedRune(' ')
	b.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if taps != 3 {
		t.Errorf("taps = %d, want 3", taps)
	}
	if r.icon.Resource() != icons.Get(icons.KVM, icons.Toolbar) || r.chevron.Visible() || !r.badge.Hidden {
		t.Error("normal state: wrong symbol, or chevron/badge shown")
	}

	b.SetActive(true)
	if r.icon.Resource() != icons.Get(icons.KVM, icons.Accent) || !same(r.bg.FillColor, style.Color(style.ColorNameActive)) {
		t.Error("active: symbol not accent or background missing")
	}
	b.SetActive(false)
	b.SetOpen(true)
	if r.icon.Resource() != icons.Get(icons.KVM, icons.MenuOpen) || !same(r.bg.FillColor, style.Color("pressed")) {
		t.Error("open: symbol not menu-open or background not pressed")
	}
	b.SetOpen(false)

	b.Disable()
	test.Tap(b)
	if taps != 3 {
		t.Error("disabled button tapped")
	}
	if r.icon.Translucency < 0.64 || r.icon.Translucency > 0.66 {
		t.Errorf("disabled translucency %v, want 0.65", r.icon.Translucency)
	}
}

func TestToolbarMenuButtonWithDotAndBadge(t *testing.T) {
	themedApp(t)
	p := NewToolbarMenuButton(icons.Power, "Power", nil)
	q := NewToolbarMenuButton(icons.KVM, "Quality", nil)
	q.SetBadge("HQ")
	test.NewTempWindow(t, fyne.NewContainerWithoutLayout(p, q))
	pr := test.WidgetRenderer(p).(*toolbarButtonRenderer)
	qr := test.WidgetRenderer(q).(*toolbarButtonRenderer)

	if !pr.chevron.Visible() || p.Dot() != DotNone {
		t.Error("menu button without chevron, or dot without SetDot")
	}
	p.SetDot(DotOn)
	if p.Dot() != DotOn || len(pr.Objects()) != 7 {
		t.Errorf("dot not shown: %v, %d objects", p.Dot(), len(pr.Objects()))
	}
	if qr.icon.Visible() || qr.badge.Hidden || qr.badge.Text != "HQ" {
		t.Error("badge does not replace the symbol")
	}
	p.Tapped(nil) // nil OnTapped must not panic
}

func TestFormCard(t *testing.T) {
	themedApp(t)
	host := NewFormRow("Host", canvas.NewText("192.0.2.10", color.White))
	name := NewFormRow("Name", canvas.NewText("Lab", color.White))
	tls := NewIconRow(icons.Lock, "Use TLS", NewSwitch(nil))
	c := NewFormCard(host, name, tls)
	test.NewTempWindow(t, c)
	c.Resize(fyne.NewSize(400, c.MinSize().Height))

	// 3 rows of 44, 2 hairlines, 1 px border top and bottom.
	if h := c.MinSize().Height; h != 3*44+2+2 {
		t.Errorf("height %v", h)
	}
	if name.Position().Y != 1+44+1 || tls.Size().Height != 44 {
		t.Errorf("row 2 at %v, row 3 height %v", name.Position().Y, tls.Size().Height)
	}
	name.Hide()
	c.Refresh()
	if h := c.MinSize().Height; h != 2*44+1+2 {
		t.Errorf("hidden row still counted: %v", h)
	}
	r := test.WidgetRenderer(c).(*formCardRenderer)
	visible := 0
	for _, s := range r.seps {
		if s.Visible() {
			visible++
		}
	}
	if visible != 1 {
		t.Errorf("%d separators visible, want 1", visible)
	}

	// The switch in the trailing slot is right aligned with 14 px padding.
	sw := tls.(*rowContainer).trailing
	if x := sw.Position().X + sw.Size().Width; x != 400-2-14 {
		t.Errorf("switch ends at %v", x)
	}
}

func TestSidebarRow(t *testing.T) {
	themedApp(t)
	taps, doubles := 0, 0
	r := NewSidebarRow("A very long device name that does not fit", "host.example.com")
	r.OnTapped = func() { taps++ }
	r.OnDoubleTapped = func() { doubles++ }
	test.NewTempWindow(t, r)
	r.Resize(fyne.NewSize(200, r.MinSize().Height))
	sr := test.WidgetRenderer(r).(*sidebarRowRenderer)

	test.Tap(r)
	test.DoubleTap(r)
	if taps != 1 || doubles != 1 {
		t.Errorf("taps %d, double taps %d", taps, doubles)
	}
	if !strings.HasSuffix(sr.name.Text, "…") || sr.host.Text != "host.example.com" {
		t.Errorf("name %q, host %q", sr.name.Text, sr.host.Text)
	}
	if h := r.MinSize().Height; h < 44 || h > 48 {
		t.Errorf("row height %v, want about 44 (30 tile + 2 x 7)", h)
	}
	if !same(sr.bg.FillColor, color.Transparent) || !same(sr.name.Color, style.Color("foreground")) {
		t.Error("unselected colors")
	}

	r.SetSelected(true)
	r.SetDot(DotOn)
	if !same(sr.bg.FillColor, style.Color("primary")) || !same(sr.name.Color, color.White) || sr.icon.Resource() != icons.Get(icons.Server, icons.White) {
		t.Error("selected: background, text or symbol wrong")
	}
	fill, _, _ := r.dot.colors()
	if !same(fill, style.CurrentStatusColor(style.StatusOn)) {
		t.Error("selected: dot lost its color")
	}
	r.Set("Lab", "192.0.2.20")
	if sr.name.Text != "Lab" || r.Host() != "192.0.2.20" {
		t.Errorf("Set: %q %q", sr.name.Text, r.Host())
	}
}

func TestStatusDot(t *testing.T) {
	themedApp(t)
	d := NewStatusDot(DotNone, 8)
	test.NewTempWindow(t, d)
	cases := []struct {
		s      DotState
		fill   color.Color
		stroke bool
	}{
		{DotNone, color.Transparent, false},
		{DotOn, style.CurrentStatusColor(style.StatusOn), false},
		{DotOff, style.CurrentStatusColor(style.StatusOff), false},
		{DotAsleep, style.CurrentStatusColor(style.StatusStandby), false},
		{DotUnknown, color.Transparent, true},
	}
	for _, c := range cases {
		d.SetState(c.s)
		fill, stroke, w := d.colors()
		if !same(fill, c.fill) || (w > 0) != c.stroke {
			t.Errorf("state %d: fill %v, stroke %v", c.s, fill, w)
		}
		if c.s == DotUnknown && !same(stroke, style.CurrentStatusColor(style.StatusUnknown)) {
			t.Errorf("unknown ring %v, want the gray of the palette", stroke)
		}
	}
	d.SetState(DotOn)
	if d.MinSize().Width != 8 {
		t.Errorf("size %v", d.MinSize())
	}
	d.SetBorder(color.Black)
	if d.MinSize().Width != 11 {
		t.Errorf("size with ring %v, want 8 + 2 x 1.5", d.MinSize())
	}
}

func TestStatusDotSelected(t *testing.T) {
	themedApp(t)
	d := NewStatusDot(DotOn, 8)
	test.NewTempWindow(t, d)
	d.SetSelected(true)
	for _, c := range []struct {
		s    DotState
		fill color.Color
	}{
		{DotOn, style.CurrentStatusColor(style.StatusOn)},
		{DotOff, style.CurrentStatusColor(style.StatusOff)},
		{DotAsleep, style.CurrentStatusColor(style.StatusStandby)},
	} {
		d.SetState(c.s)
		fill, stroke, w := d.colors()
		if !same(fill, c.fill) || !same(stroke, style.Color(style.ColorNameWhite)) || w <= 0 || w > 1.5 {
			t.Errorf("state %d selected: fill %v, stroke %v, width %v", c.s, fill, stroke, w)
		}
		if d.MinSize().Width != 8 {
			t.Errorf("state %d selected: size %v, want unchanged 8", c.s, d.MinSize())
		}
	}
	d.SetState(DotUnknown)
	fill, stroke, w := d.colors()
	if !same(fill, color.Transparent) || !same(stroke, style.Color(style.ColorNameSelectedText2)) || w != dotRing {
		t.Errorf("unknown selected: fill %v, stroke %v, width %v", fill, stroke, w)
	}
	d.SetState(DotNone)
	if fill, _, w := d.colors(); !same(fill, color.Transparent) || w != 0 {
		t.Error("none selected draws something")
	}
}

func TestEllipsize(t *testing.T) {
	themedApp(t)
	st := fyne.TextStyle{}
	if got := ellipsize("short", 13, st, 500); got != "short" {
		t.Errorf("got %q", got)
	}
	got := ellipsize("a long text that needs shortening", 13, st, 60)
	if !strings.HasSuffix(got, "…") || fyne.MeasureText(got, 13, st).Width > 60 {
		t.Errorf("got %q", got)
	}
	if ellipsize("x", 13, st, 0) != "" {
		t.Error("zero width")
	}
}

func TestFormCardEntriesHaveNoBorder(t *testing.T) {
	themedApp(t)
	e := widget.NewEntry()
	e.SetText("192.0.2.10") // renderer exists before the card applies its theme
	test.NewTempWindow(t, NewFormCard(NewFormRow("Host", e)))
	if _, _, _, a := e.Theme().Color(theme.ColorNameInputBorder, theme.VariantDark).RGBA(); a != 0 {
		t.Error("entry inside a card does not use the card theme")
	}
	for _, o := range test.WidgetRenderer(e).Objects() {
		if r, ok := o.(*canvas.Rectangle); ok && r.StrokeWidth > 0 {
			if _, _, _, a := r.StrokeColor.RGBA(); a != 0 {
				t.Error("entry inside a card still draws its border")
			}
		}
	}
}

// The unknown state on the Power button: the ring sits inside the 7 px dot on a disk in the
// button background, instead of a hollow ring over the symbol.
func TestStatusDotUnknownWithBorder(t *testing.T) {
	themedApp(t)
	b := NewToolbarMenuButton(icons.Power, "Power", nil)
	test.NewTempWindow(t, b)
	b.SetDot(DotUnknown)
	r := test.WidgetRenderer(b.dot).(*dotRenderer)
	if !same(r.circle.FillColor, style.Color(style.ColorNameToolbar)) || r.circle.StrokeWidth != 0 || r.inner.Hidden ||
		r.inner.Size().Width != style.StatusDotSizeSmall || b.dot.MinSize().Width != style.StatusDotSizeSmall+3 {
		t.Fatalf("fill %v stroke %v inner %v %v", r.circle.FillColor, r.circle.StrokeWidth, r.inner.Hidden, r.inner.Size())
	}
	b.SetDot(DotOn)
	if !r.inner.Hidden || !same(r.circle.FillColor, style.CurrentStatusColor(style.StatusOn)) {
		t.Fatal("inner ring shown for a known state")
	}
	// The ring around the dot follows the item background (SessionA2.html: #3A3A3C while open).
	b.SetOpen(true)
	if same(r.circle.StrokeColor, style.Color(style.ColorNameToolbar)) {
		t.Fatal("border did not follow the open background")
	}
}

func TestMenuPopup(t *testing.T) {
	themedApp(t)
	var ran []string
	item := func(label string) *fyne.MenuItem {
		return fyne.NewMenuItem(label, func() { ran = append(ran, label) })
	}
	a, b, c := item("A"), item("B"), item("C")
	b.Disabled = true
	m := fyne.NewMenu("", a, fyne.NewMenuItemSeparator(), b, c)
	w := test.NewTempWindow(t, canvas.NewRectangle(color.Black))
	w.Resize(fyne.NewSize(400, 300))
	p := NewMenuPopup(m, w.Canvas())
	dismissed := 0
	p.OnDismiss = func() { dismissed++ }
	p.ShowAtPosition(fyne.NewPos(20, 30))

	if !p.Visible() || w.Canvas().Overlays().Top() != p || w.Canvas().Focused() != p {
		t.Fatal("menu not shown or not focused")
	}
	if p.panel.Position() != fyne.NewPos(20, 30) || p.panel.Size().Width != menuWidth {
		t.Fatalf("panel at %v size %v", p.panel.Position(), p.panel.Size())
	}
	// 5 padding, rows of 30, separator 1 + 2 x 4, 1 px between.
	if want := 2*menuPad + 3*menuRowHeight + 9 + 3*menuRowGap; p.panel.Size().Height != want {
		t.Fatalf("height %v, want %v", p.panel.Size().Height, want)
	}
	if p.Item(1) != nil || p.rows[0].Size().Height != menuRowHeight {
		t.Fatal("separator or row size")
	}
	rr := test.WidgetRenderer(p.rows[0]).(*menuRowRenderer)
	if rr.icon.Size().Width != 16 {
		t.Fatalf("icon %v", rr.icon.Size())
	}

	// A disabled row does nothing; a tap on the panel padding keeps the menu open.
	test.Tap(p.Item(2))
	test.TapAt(p, fyne.NewPos(22, 32))
	if len(ran) != 0 || dismissed != 0 {
		t.Fatalf("ran %v dismissed %d", ran, dismissed)
	}
	// Keyboard: Down skips separator and disabled rows, Return runs the item and closes.
	p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if p.active != 3 || !p.rows[3].highlighted() || p.rows[0].highlighted() {
		t.Fatalf("active %d", p.active)
	}
	p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if len(ran) != 1 || ran[0] != "C" || dismissed != 1 || p.Visible() || w.Canvas().Overlays().Top() != nil {
		t.Fatalf("ran %v dismissed %d", ran, dismissed)
	}
	// Tap outside and Escape dismiss without running anything.
	p.ShowAtPosition(fyne.NewPos(20, 30))
	test.TapAt(p, fyne.NewPos(390, 290))
	p.ShowAtPosition(fyne.NewPos(20, 30))
	p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	p.Dismiss() // already closed: no second OnDismiss
	if dismissed != 3 || len(ran) != 1 {
		t.Fatalf("dismissed %d ran %v", dismissed, ran)
	}
	// Tapping a row runs it; Checked shows a check mark in the symbol column.
	a.Checked = true
	p.ShowAtPosition(fyne.NewPos(20, 30))
	if !rr.icon.Visible() || rr.icon.Resource() == nil {
		t.Fatal("no check mark")
	}
	test.Tap(p.Item(0))
	if len(ran) != 2 || ran[1] != "A" || p.Visible() {
		t.Fatalf("ran %v", ran)
	}
}

// The GLFW driver delays Tapped until the double-click interval has passed, so the primary
// MouseDown selects at once; the Tapped that follows must not select a second time.
func TestSidebarRowSelectsOnMouseDown(t *testing.T) {
	themedApp(t)
	taps := 0
	r := NewSidebarRow("Lab", "host.example.com")
	r.OnTapped = func() { taps++ }
	test.NewTempWindow(t, r)

	r.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})
	if taps != 0 {
		t.Fatal("secondary button selected")
	}
	r.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	if taps != 1 {
		t.Fatalf("MouseDown: %d selections", taps)
	}
	r.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	test.Tap(r) // the delayed Tapped of the same click
	if taps != 1 {
		t.Fatalf("Tapped selected twice: %d", taps)
	}
	test.Tap(r) // Tapped without a MouseDown (touch, accessibility) still selects
	if taps != 2 {
		t.Fatalf("lone Tapped: %d", taps)
	}
}

// A double click ends in DoubleTapped, not in Tapped: the pending MouseDown mark must not
// swallow the next lone Tapped (touch, accessibility).
func TestSidebarRowDoubleTapClearsMouseDownMark(t *testing.T) {
	themedApp(t)
	taps := 0
	r := NewSidebarRow("Lab", "host.example.com")
	r.OnTapped = func() { taps++ }
	test.NewTempWindow(t, r)
	primary := &desktop.MouseEvent{Button: desktop.MouseButtonPrimary}
	r.MouseDown(primary)
	r.MouseUp(primary)
	r.MouseDown(primary)
	r.MouseUp(primary)
	r.DoubleTapped(nil)
	before := taps
	test.Tap(r)
	if taps != before+1 {
		t.Fatalf("lone Tapped after a double click was swallowed (%d -> %d)", before, taps)
	}
}

// A press that never ends in Tapped (dragged off the row) must not swallow a Tapped that
// comes long after the release.
func TestSidebarRowStaleMouseDownMarkExpires(t *testing.T) {
	themedApp(t)
	taps := 0
	r := NewSidebarRow("Lab", "host.example.com")
	r.OnTapped = func() { taps++ }
	test.NewTempWindow(t, r)
	primary := &desktop.MouseEvent{Button: desktop.MouseButtonPrimary}
	r.MouseDown(primary)
	r.MouseUp(primary)
	r.upAt = time.Now().Add(-time.Minute) // no Tapped followed this release
	before := taps
	test.Tap(r)
	if taps != before+1 {
		t.Fatalf("stale mark swallowed a Tapped (%d -> %d)", before, taps)
	}
}

// A menu taller than the canvas keeps its top below the button, scrolls its rows and brings the
// keyboard row into view.
func TestMenuPopupScrollsWhenTallerThanCanvas(t *testing.T) {
	themedApp(t)
	var items []*fyne.MenuItem
	for i := range 40 {
		items = append(items, fyne.NewMenuItem(string(rune('A'+i%26)), nil))
	}
	w := test.NewTempWindow(t, canvas.NewRectangle(color.Black))
	w.Resize(fyne.NewSize(400, 400))
	p := NewMenuPopup(fyne.NewMenu("", items...), w.Canvas())
	p.ShowAtPosition(fyne.NewPos(20, 60))
	if h := p.panel.Size().Height; h > 400-2*menuEdge || p.panel.Position().Y != 60 {
		t.Fatalf("panel at %v height %v", p.panel.Position(), h)
	}
	if p.panel.Position().Y+p.panel.Size().Height > 400 {
		t.Fatalf("panel leaves the canvas: %v %v", p.panel.Position(), p.panel.Size())
	}
	if p.scroll.Content.MinSize().Height <= p.scroll.Size().Height {
		t.Fatal("rows are not taller than the view")
	}
	for range 30 {
		p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	}
	last := p.rows[p.active]
	if top, bottom := last.Position().Y, last.Position().Y+last.Size().Height; top < p.scroll.Offset.Y || bottom > p.scroll.Offset.Y+p.scroll.Size().Height {
		t.Fatalf("active row %v..%v outside view %v+%v", top, bottom, p.scroll.Offset.Y, p.scroll.Size().Height)
	}
}
