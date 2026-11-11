package widgets

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// TestPrimaryButtonMeasures is the proof for #61: the button has the design's measures
// (ManagerA.html): 36 high, 18 px side padding, 16 px symbol 8 px from the text, text 13.5
// semibold.
func TestPrimaryButtonMeasures(t *testing.T) {
	themedApp(t)
	tapped := 0
	b := NewPrimaryButton("Connect", icons.GetStroke(icons.Connect, icons.White, 2), func() { tapped++ })
	b.IconTrailing = true
	test.NewTempWindow(t, b)
	r := test.WidgetRenderer(b).(*primaryButtonRenderer)

	textW := fyne.MeasureText("Connect", 13.5, fyne.TextStyle{Bold: true}).Width
	min := b.MinSize()
	if min.Height != 36 || min.Width != 18+textW+8+16+18 {
		t.Fatalf("MinSize %v, want 36 high and 18+%.1f+8+16+18 wide", min, textW)
	}
	if r.text.TextSize != 13.5 || !r.text.TextStyle.Bold {
		t.Errorf("text %v bold=%v", r.text.TextSize, r.text.TextStyle.Bold)
	}
	b.Resize(min)
	if r.text.Position().X != 18 || r.icon.Position().X != 18+textW+8 || r.icon.Size() != fyne.NewSquareSize(16) {
		t.Errorf("trailing: text at %v, icon at %v size %v", r.text.Position(), r.icon.Position(), r.icon.Size())
	}
	if r.bg.CornerRadius != 9 || style.PrimaryButtonRadius != 9 {
		t.Errorf("radius %v", r.bg.CornerRadius)
	}

	// Symbol first (Connect KVM) and no symbol (Cancel): the padding stays 18.
	b.IconTrailing = false
	b.Refresh()
	if r.icon.Position().X != 18 || r.text.Position().X != 18+16+8 {
		t.Errorf("leading: icon at %v, text at %v", r.icon.Position(), r.text.Position())
	}
	b.SetIcon(nil)
	b.Resize(b.MinSize())
	if b.MinSize().Width != 36+textW || r.icon.Visible() || r.text.Position().X != 18 {
		t.Errorf("no icon: %v, text at %v", b.MinSize(), r.text.Position())
	}

	test.Tap(b)
	b.TypedRune(' ')
	b.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	b.Disable()
	test.Tap(b)
	if tapped != 3 {
		t.Errorf("tapped %d, want 3", tapped)
	}
}

func TestPrimaryButtonStates(t *testing.T) {
	themedApp(t)
	b := NewPrimaryButton("Connect KVM", icons.GetStroke(icons.KVM, icons.White, 2), nil)
	test.NewTempWindow(t, b)
	r := test.WidgetRenderer(b).(*primaryButtonRenderer)
	if !same(r.bg.FillColor, style.Color("primary")) || !same(r.text.Color, style.Color(style.ColorNameWhite)) {
		t.Errorf("high importance: %v on %v", r.text.Color, r.bg.FillColor)
	}
	b.Importance = widget.MediumImportance
	b.Refresh()
	if same(r.bg.FillColor, style.Color("primary")) || !same(r.text.Color, style.Color("foreground")) {
		t.Errorf("medium importance: %v on %v", r.text.Color, r.bg.FillColor)
	}
	b.Importance = widget.HighImportance
	b.Disable()
	if same(r.bg.FillColor, style.Color("primary")) || r.icon.Translucency < 0.64 {
		t.Errorf("disabled: %v, %v", r.bg.FillColor, r.icon.Translucency)
	}
}

// TestPrimaryButtonKeyboard: Return, Enter and Space each trigger exactly once; other keys
// and runes do not.
func TestPrimaryButtonKeyboard(t *testing.T) {
	themedApp(t)
	tapped := 0
	b := NewPrimaryButton("Connect", nil, func() { tapped++ })
	test.NewTempWindow(t, b)

	b.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if tapped != 1 {
		t.Errorf("Return: tapped %d, want 1", tapped)
	}
	b.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnter})
	if tapped != 2 {
		t.Errorf("Enter: tapped %d, want 2", tapped)
	}
	b.TypedRune(' ')
	if tapped != 3 {
		t.Errorf("Space: tapped %d, want 3", tapped)
	}
	b.TypedRune('a')
	b.TypedKey(&fyne.KeyEvent{Name: fyne.KeyA})
	b.TypedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if tapped != 3 {
		t.Errorf("other keys: tapped %d, want 3", tapped)
	}
}

func TestPrimaryButtonDisabledDoesNotTrigger(t *testing.T) {
	themedApp(t)
	tapped := 0
	b := NewPrimaryButton("Connect", nil, func() { tapped++ })
	test.NewTempWindow(t, b)
	b.Disable()

	test.Tap(b)
	b.TypedRune(' ')
	b.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	b.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnter})
	if tapped != 0 {
		t.Errorf("disabled button triggered %d times", tapped)
	}
	b.Enable()
	test.Tap(b)
	if tapped != 1 {
		t.Errorf("enabled again: tapped %d, want 1", tapped)
	}
}

func TestPrimaryButtonDisabledSkipsFocus(t *testing.T) {
	themedApp(t)
	b := NewPrimaryButton("Connect", nil, func() {})
	w := test.NewTempWindow(t, b)
	c := w.Canvas()

	c.FocusNext()
	if c.Focused() != b {
		t.Fatalf("enabled button not focused via FocusNext: %v", c.Focused())
	}
	c.Unfocus()
	b.Disable()
	c.FocusNext()
	if c.Focused() != nil {
		t.Errorf("disabled button got focus: %v", c.Focused())
	}
}

// TestPrimaryButtonRegularText: Metrics.Regular draws the text in the regular weight (the
// bordered buttons of the Media popover, Media.dc.html .btn) and measures it so.
func TestPrimaryButtonRegularText(t *testing.T) {
	themedApp(t)
	b := NewPrimaryButton("Unmount", nil, nil)
	b.Metrics = &ButtonMetrics{Height: 34, PaddingH: 12, TextSize: 13, Regular: true}
	test.NewTempWindow(t, b)
	r := test.WidgetRenderer(b).(*primaryButtonRenderer)
	if r.text.TextStyle.Bold {
		t.Fatal("regular text drawn bold")
	}
	if w := fyne.MeasureText("Unmount", 13, fyne.TextStyle{}).Width; b.MinSize().Width != 24+w {
		t.Fatalf("MinSize %v, want 24+%.1f wide", b.MinSize(), w)
	}
}
