package widgets

import (
	"image/color"
	"net/url"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

func TestLinkTapRunsActionAndSizesWithIcon(t *testing.T) {
	themedApp(t)
	u, _ := url.Parse("https://example.com")
	l := NewLink("example.com", u)
	plain := l.MinSize()
	calls := 0
	l.OnTapped = func() { calls++ }
	test.Tap(l)
	if calls != 1 {
		t.Fatalf("OnTapped ran %d times", calls)
	}
	l.Icon, l.IconSize, l.IconGap = icons.Get(icons.ExternalLink, icons.Link), 12, 5
	l.Refresh()
	if got := l.MinSize().Width - plain.Width; got != 17 {
		t.Errorf("icon adds %v, want 12 + 5", got)
	}
	l.Height, l.PadH = 28, 10
	if m := l.MinSize(); m.Height != 28 || m.Width != plain.Width+17+20 {
		t.Errorf("flat button size %v", m)
	}
}

func TestLinkHoverColorOnlyWhereSet(t *testing.T) {
	themedApp(t)
	textColor := func(l *Link) color.Color {
		return test.WidgetRenderer(l).Objects()[0].(*canvas.Text).Color
	}
	blue, light := style.Color(style.ColorNameLink), style.Color(style.ColorNameLinkHover)
	l := NewLink("a", nil)
	l.Color, l.HoverColor = style.ColorNameLink, style.ColorNameLinkHover
	l.Refresh()
	if textColor(l) != blue {
		t.Fatalf("rest color %v", textColor(l))
	}
	l.MouseIn(nil)
	if textColor(l) != light {
		t.Errorf("hover color %v, want %v", textColor(l), light)
	}
	l.MouseOut()
	if textColor(l) != blue {
		t.Errorf("after hover %v", textColor(l))
	}
	// Without a hover color the text keeps its color (credits, footer links).
	plain := NewLink("b", nil)
	plain.Color = "foreground"
	plain.Refresh()
	plain.MouseIn(nil)
	if textColor(plain) != style.Color("foreground") {
		t.Errorf("link without hover color changed to %v", textColor(plain))
	}
}

func TestParagraphWrapsAtWidthWithLineHeight(t *testing.T) {
	themedApp(t)
	text := "OpenLAMT is a free, independent project. If it saves you a trip to the server room, a coffee helps keep it going."
	p := NewParagraph(text, 12.5, 412, 18, style.ColorNameTextSecondary)
	lines := p.Lines()
	if len(lines) != 2 {
		t.Fatalf("%d lines: %q", len(lines), lines)
	}
	if strings.Join(lines, " ") != text {
		t.Errorf("lines lose text: %q", lines)
	}
	for _, l := range lines {
		if w := fyne.MeasureText(l, 12.5, fyne.TextStyle{}).Width; w > 412 {
			t.Errorf("line %q is %v wide", l, w)
		}
	}
	if m := p.MinSize(); m.Width != 412 || m.Height != 36 {
		t.Errorf("MinSize %v, want 412 x 36", m)
	}
	p.Width = 2000
	p.Refresh()
	if p.MinSize().Height != 18 {
		t.Errorf("one line is %v high", p.MinSize().Height)
	}
}

func TestDropdownSelectsAndOpensMenu(t *testing.T) {
	themedApp(t)
	d := NewDropdown([]string{"Automatic", "DE", "US"})
	if d.Selected != "Automatic" || d.SelectedIndex() != 0 {
		t.Fatalf("start %q %d", d.Selected, d.SelectedIndex())
	}
	var got []string
	d.OnChanged = func(s string) { got = append(got, s) }
	d.SetSelected("nope") // unknown: ignored
	d.SetSelectedIndex(7)
	d.SetSelectedIndex(2)
	d.SetSelected("US") // unchanged: no second call
	if d.Selected != "US" || len(got) != 1 || got[0] != "US" {
		t.Fatalf("%q %v", d.Selected, got)
	}
	if m := d.MinSize(); m.Height != 26 {
		t.Errorf("height %v, want 26", m.Height)
	}
	w := test.NewWindow(d)
	defer w.Close()
	w.Resize(fyne.NewSize(400, 300))
	test.Tap(d)
	if d.Menu() == nil || !d.Menu().Visible() {
		t.Fatal("tap did not open the menu")
	}
	test.Tap(d.Menu().Item(0))
	if d.Selected != "Automatic" || d.Menu().Visible() {
		t.Fatalf("chose %q, menu visible %v", d.Selected, d.Menu().Visible())
	}
	d.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if !d.Menu().Visible() {
		t.Fatal("Down did not open the menu")
	}
}

func TestDropdownWidthIsThatOfTheWidestOption(t *testing.T) {
	themedApp(t)
	d := NewDropdown([]string{"5 minutes", "Never", "Automatic (system language)"})
	wide := d.MinSize().Width
	d.SetSelected("Never")
	if d.MinSize().Width != wide {
		t.Errorf("width changed from %v to %v with the choice", wide, d.MinSize().Width)
	}
}

func TestPrimaryButtonMetricsChangeMeasuresOnly(t *testing.T) {
	themedApp(t)
	def := NewPrimaryButton("Connect", nil, nil)
	if m := def.MinSize(); m.Height != style.PrimaryButtonHeight {
		t.Fatalf("default height %v", m.Height)
	}
	pill := NewPrimaryButton("Connect", icons.Get(icons.Coffee, icons.White), nil)
	pill.Metrics = &ButtonMetrics{Height: 30, Radius: 15, PaddingH: 14, IconSize: 15, Gap: 7, TextSize: 12.5}
	m := pill.MinSize()
	text := fyne.MeasureText("Connect", 12.5, fyne.TextStyle{Bold: true}).Width
	if m.Height != 30 || m.Width != 28+text+7+15 {
		t.Errorf("pill %v, want 30 x %v", m, 28+text+7+15)
	}
	// The default button is unchanged by the existence of metrics on another one.
	if def.MinSize().Height != style.PrimaryButtonHeight {
		t.Error("default button changed")
	}
	if got := pill.metrics().Neutral; got != "button" {
		t.Errorf("neutral fill %v", got)
	}
	var esc int
	pill.OnEscape = func() { esc++ }
	pill.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if esc != 1 {
		t.Error("Esc not forwarded")
	}
}

func TestCellGridLaysOutRowsAndWidensTheLongerColumn(t *testing.T) {
	themedApp(t)
	short := func() fyne.CanvasObject { return NewCell(widget.NewLabel("a"), NewBadge("MIT")) }
	long := NewCell(widget.NewLabel(strings.Repeat("long ", 25)), NewBadge("Apache-2.0"))
	g := NewCellGrid(2, 42, short(), long, short(), short(), short(), short())
	g.Resize(fyne.NewSize(512, g.MinSize().Height))
	if g.MinSize().Height != 3*42+2+2 {
		t.Errorf("height %v, want 3 rows of 42 with hairlines and border", g.MinSize().Height)
	}
	r := test.WidgetRenderer(g).(*gridRenderer)
	w := r.colWidths(512)
	if w[0]+w[1]+3 != 512 || w[1] <= w[0] {
		t.Errorf("column widths %v: the column with the long cell must be wider and all must fill 512", w)
	}
	if rows := g.rows(); rows != 3 {
		t.Errorf("%d rows", rows)
	}
}

func TestFormCardBackgroundOverride(t *testing.T) {
	themedApp(t)
	c := NewFormCard(widget.NewLabel("x"))
	c.Background = style.ColorNameDialogCard
	if r := test.WidgetRenderer(c).(*formCardRenderer); r.bg.FillColor != style.Color(style.ColorNameDialogCard) {
		t.Errorf("fill %v", r.bg.FillColor)
	}
	if r := test.WidgetRenderer(NewFormCard()).(*formCardRenderer); r.bg.FillColor != style.Color(style.ColorNameCard) {
		t.Errorf("default fill %v", r.bg.FillColor)
	}
}

func TestAppIconDrawsAtPixelSize(t *testing.T) {
	themedApp(t)
	a := NewAppIcon(icons.AppSimple, 36)
	if a.MinSize() != fyne.NewSquareSize(36) {
		t.Fatalf("MinSize %v", a.MinSize())
	}
	img := a.generate(72, 72)
	if img.Bounds().Dx() != 72 || a.generate(72, 72) != img {
		t.Error("image not drawn at the asked size or not reused")
	}
}

// widget.Hyperlink was reachable by Tab; the link replacing it must be too: Tab focuses it,
// Enter and Space open it, and the focus shows as a ring.
func TestLinkIsKeyboardReachable(t *testing.T) {
	themedApp(t)
	l := NewLink("example.com", nil)
	calls := 0
	l.OnTapped = func() { calls++ }
	w := test.NewTempWindow(t, l)
	c := w.Canvas()
	c.FocusNext()
	if c.Focused() != l {
		t.Fatalf("Tab did not focus the link: %v", c.Focused())
	}
	ring := func() bool {
		for _, o := range test.WidgetRenderer(l).Objects() {
			if r, ok := o.(*canvas.Rectangle); ok && r.StrokeWidth > 0 {
				return !r.Hidden
			}
		}
		t.Fatal("no focus ring")
		return false
	}
	if !ring() {
		t.Error("focused link shows no ring")
	}
	l.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	l.TypedRune(' ')
	if calls != 2 {
		t.Errorf("Enter and Space opened %d times, want 2", calls)
	}
	c.Unfocus()
	if ring() {
		t.Error("ring stays after focus loss")
	}
}
