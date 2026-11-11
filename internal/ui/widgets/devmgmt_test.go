package widgets

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

func TestSegmentedToolbarTapsAndReselect(t *testing.T) {
	themedApp(t)
	var changed, reselected []int
	s := NewSegmented(SegmentedToolbar, []SegmentOption{{Icon: icons.KVM, Label: "KVM"}, {Icon: icons.Console, Label: "Console"}},
		func(i int) { changed = append(changed, i) })
	s.OnReselected = func(i int) { reselected = append(reselected, i) }
	test.NewTempWindow(t, s)
	s.Resize(s.MinSize())

	if h := s.MinSize().Height; h != style.ToolbarItemHeight {
		t.Errorf("toolbar segment height %v, want %v", h, style.ToolbarItemHeight)
	}
	if s.Selected() != 0 {
		t.Fatalf("selected %d, want 0", s.Selected())
	}
	b1 := s.segmentBounds(1)
	test.TapAt(s, fyne.NewPos(b1.pos.X+b1.size.Width/2, b1.pos.Y+b1.size.Height/2))
	if s.Selected() != 1 || len(changed) != 1 || changed[0] != 1 || len(reselected) != 0 {
		t.Fatalf("tap on segment 1: selected %d changed %v reselected %v", s.Selected(), changed, reselected)
	}
	s.Tap(1) // the active segment again
	if len(changed) != 1 || len(reselected) != 1 || reselected[0] != 1 {
		t.Fatalf("tap on the active segment: changed %v reselected %v", changed, reselected)
	}
	s.SetSelected(0) // programmatic: no callbacks
	if s.Selected() != 0 || len(changed) != 1 || len(reselected) != 1 {
		t.Errorf("SetSelected ran callbacks: %v %v", changed, reselected)
	}
	s.Disable()
	s.Tap(1)
	test.TapAt(s, fyne.NewPos(b1.pos.X+2, b1.pos.Y+b1.size.Height/2))
	if s.Selected() != 0 || len(changed) != 1 {
		t.Errorf("disabled segmented control changed: %d %v", s.Selected(), changed)
	}
	s.Enable()

	r := test.WidgetRenderer(s).(*segmentedRenderer)
	if !same(r.track.FillColor, style.Color(style.ColorNameSegment)) || r.track.CornerRadius != 11 {
		t.Errorf("track %v radius %v", r.track.FillColor, r.track.CornerRadius)
	}
	on, off := r.segs[0], r.segs[1]
	if !same(on.bg.FillColor, style.Color(style.ColorNameSegmentActive)) || on.bg.CornerRadius != 8 || on.shadow.Hidden {
		t.Errorf("active segment bg %v radius %v shadow hidden %v", on.bg.FillColor, on.bg.CornerRadius, on.shadow.Hidden)
	}
	if !same(on.text.Color, style.Color(style.ColorNameAccentText)) || on.icon.Resource() != icons.Get(icons.KVM, icons.AccentText) {
		t.Errorf("active segment caption %v icon %v", on.text.Color, on.icon.Resource())
	}
	if !off.bg.Hidden || off.icon.Resource() != icons.Get(icons.Console, icons.Toolbar) || off.icon.Size().Width != style.ToolbarIconSize {
		t.Errorf("inactive segment: bg hidden %v icon %v size %v", off.bg.Hidden, off.icon.Resource(), off.icon.Size())
	}
}

func TestSegmentedTabsShareTheWidth(t *testing.T) {
	themedApp(t)
	s := NewSegmented(SegmentedTabs, TextOptions("Hardware", "Events", "Audit", "Settings", "Users"), nil)
	test.NewTempWindow(t, s)
	s.Resize(fyne.NewSize(368, s.MinSize().Height))
	w0 := s.segmentBounds(0).size.Width
	for i := 1; i < 5; i++ {
		if b := s.segmentBounds(i); b.size.Width != w0 {
			t.Errorf("segment %d is %v wide, segment 0 %v", i, b.size.Width, w0)
		}
	}
	last := s.segmentBounds(4)
	if end := last.pos.X + last.size.Width; end < 368-3-0.5 || end > 368-3+0.5 {
		t.Errorf("segments end at %v, want 365 (3 px padding)", end)
	}
	if s.Size().Height > 40 {
		t.Errorf("tabs too high: %v", s.Size())
	}
}

func TestSegmentedTextIsCompact(t *testing.T) {
	themedApp(t)
	s := NewSegmented(SegmentedText, TextOptions("None", "KVM only", "All"), nil)
	test.NewTempWindow(t, s)
	min := s.MinSize()
	labels := 0.0
	for _, l := range []string{"None", "KVM only", "All"} {
		labels += float64(fyne.MeasureText(l, 12, fyne.TextStyle{}).Width)
	}
	if want := float32(labels) + 3*20 + 2*2; min.Width < want-1 || min.Width > want+1 {
		t.Errorf("min width %v, want %v", min.Width, want)
	}
	s.SetSelected(1)
	r := test.WidgetRenderer(s).(*segmentedRenderer)
	if r.segs[0].icon.Visible() || !same(r.segs[1].text.Color, style.Color("foreground")) || !same(r.segs[0].text.Color, style.Color(style.ColorNameControlText)) {
		t.Error("text variant: symbol shown or wrong caption colors")
	}
}

func TestChip(t *testing.T) {
	themedApp(t)
	taps := 0
	c := NewChip("Warning", func() { taps++ })
	test.NewTempWindow(t, c)
	r := test.WidgetRenderer(c).(*chipRenderer)
	h := c.MinSize().Height
	if r.bg.CornerRadius != h/2 || !same(r.bg.StrokeColor, style.Color(style.ColorNameControlBorder)) || !same(r.text.Color, style.Color(style.ColorNameControlText)) {
		t.Errorf("inactive chip: radius %v of %v, border %v, text %v", r.bg.CornerRadius, h, r.bg.StrokeColor, r.text.Color)
	}
	c.SetActive(true)
	if !c.Active() || !same(r.bg.FillColor, style.Color(style.ColorNameChipActive)) || !same(r.text.Color, style.Color(style.ColorNameChipActiveText)) {
		t.Errorf("active chip: %v on %v", r.text.Color, r.bg.FillColor)
	}
	test.Tap(c)
	c.Disable()
	test.Tap(c)
	if taps != 1 {
		t.Errorf("taps %d, want 1", taps)
	}
}

func TestFilterFieldClearButtonOnlyWithText(t *testing.T) {
	themedApp(t)
	var got []string
	f := NewFilterField(func(s string) { got = append(got, s) })
	test.NewTempWindow(t, f)
	if f.Entry.PlaceHolder != "Filter" {
		t.Errorf("placeholder %q", f.Entry.PlaceHolder)
	}
	if f.clear.Visible() {
		t.Fatal("clear button visible while empty")
	}
	test.Type(f.Entry, "boot")
	if !f.clear.Visible() || f.Text() != "boot" || got[len(got)-1] != "boot" {
		t.Fatalf("after typing: visible %v text %q changes %v", f.clear.Visible(), f.Text(), got)
	}
	test.Tap(f.clear)
	if f.clear.Visible() || f.Text() != "" || got[len(got)-1] != "" {
		t.Errorf("after clear: visible %v text %q changes %v", f.clear.Visible(), f.Text(), got)
	}
	f.SetText("x")
	if !f.clear.Visible() {
		t.Error("SetText does not show the clear button")
	}
	cr := test.WidgetRenderer(f.clear).(*clearButtonRenderer)
	if cr.circle.Size() != fyne.NewSquareSize(16) || !same(cr.circle.FillColor, style.Color(style.ColorNameFilterClear)) {
		t.Errorf("clear circle %v %v", cr.circle.Size(), cr.circle.FillColor)
	}
}

func TestPasswordFieldReveal(t *testing.T) {
	themedApp(t)
	p := NewPasswordField()
	test.NewTempWindow(t, p)
	test.Type(p.Entry, "Secret-Pass1")
	if !p.Entry.Password || p.Revealed() {
		t.Fatal("password not hidden by default")
	}
	if p.Entry.ActionItem != p.eye {
		t.Fatal("the eye is not inside the field")
	}
	test.Tap(p.eye)
	if p.Entry.Password || !p.Revealed() || p.Text() != "Secret-Pass1" {
		t.Fatalf("after reveal: password %v text %q", p.Entry.Password, p.Text())
	}
	test.Tap(p.eye)
	if !p.Entry.Password || p.Text() != "Secret-Pass1" {
		t.Errorf("after hide: password %v text %q", p.Entry.Password, p.Text())
	}
	if p.eye.MinSize() != fyne.NewSquareSize(28) || p.eye.icon.Resource() != icons.Get(icons.Eye, icons.Secondary) {
		t.Errorf("eye %v %v", p.eye.MinSize(), p.eye.icon.Resource())
	}
	handled := 0
	p.KeyFilter = func(e *fyne.KeyEvent) bool { handled++; return e.Name == fyne.KeyEscape }
	p.Entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if handled != 1 {
		t.Error("KeyFilter not asked")
	}
}

func TestPasswordDisplayIsReadOnlyMonospace(t *testing.T) {
	themedApp(t)
	p := NewPasswordDisplay("Xr7#mQ2v!pL9")
	test.NewTempWindow(t, p)
	if p.Entry != nil || p.text == nil || !p.text.TextStyle.Monospace || p.text.TextSize != 14 {
		t.Fatal("display is not a read-only monospace text of size 14")
	}
	if strings.Contains(p.text.Text, "Xr7") || p.Text() != "Xr7#mQ2v!pL9" {
		t.Fatalf("hidden display shows %q", p.text.Text)
	}
	test.Tap(p.eye)
	if p.text.Text != "Xr7#mQ2v!pL9" {
		t.Errorf("revealed display shows %q", p.text.Text)
	}
	if p.eye.MinSize() != fyne.NewSquareSize(30) || p.eye.icon.MinSize() != fyne.NewSquareSize(18) {
		t.Errorf("display eye %v icon %v", p.eye.MinSize(), p.eye.icon.MinSize())
	}
}

func TestPrimaryButtonDanger(t *testing.T) {
	themedApp(t)
	b := NewPrimaryButton("Turn Off…", nil, nil)
	b.Importance = widget.DangerImportance
	test.NewTempWindow(t, b)
	r := test.WidgetRenderer(b).(*primaryButtonRenderer)
	b.Refresh()
	if !same(r.bg.FillColor, style.Color(style.ColorNameDanger)) || !same(r.text.Color, style.Color(style.ColorNameWhite)) || !r.text.TextStyle.Bold {
		t.Errorf("danger: %v on %v", r.text.Color, r.bg.FillColor)
	}
	b.Disable()
	if !same(r.bg.FillColor, style.Color(style.ColorNameDangerDisabled)) || !same(r.text.Color, style.Color(style.ColorNameWhite)) {
		t.Errorf("disabled danger: %v on %v", r.text.Color, r.bg.FillColor)
	}
}
