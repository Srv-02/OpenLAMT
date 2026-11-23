package widgets

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

var helpFull = HelpContent{
	Title:   "Intranet domains",
	What:    "Intel AMT compares the DNS suffix it gets from DHCP with these domains. If none matches, AMT assumes it is outside your network.",
	When:    "Only with remote access from outside. On a home or lab network, leave it as it is.",
	Wrong:   "A wrong entry can make AMT stop answering on the local network.",
	Recover: "Clear the list in the BIOS (MEBx) at the device.",
}

func helpWindow(t *testing.T, c HelpContent) (fyne.Window, *InfoButton) {
	t.Helper()
	themedApp(t)
	b := NewInfoButton(c)
	w := test.NewWindow(container.NewPadded(b))
	t.Cleanup(w.Close)
	w.Resize(fyne.NewSize(600, 400))
	return w, b
}

func TestInfoButtonOpensPopoverAndClosesOnEsc(t *testing.T) {
	w, b := helpWindow(t, helpFull)
	var shown *HelpPopover
	b.OnShown = func(p *HelpPopover) { shown = p }
	if got := b.MinSize(); got.Width != helpHitSize || got.Height != helpHitSize {
		t.Errorf("min size %v", got)
	}
	test.Tap(b)
	if shown == nil || !shown.Visible() || len(w.Canvas().Overlays().List()) != 1 {
		t.Fatalf("popover not shown")
	}
	if w.Canvas().Focused() != fyne.Focusable(shown) {
		t.Error("the popover must take the focus")
	}
	if got := shown.PanelSize().Width; got != helpPopoverW {
		t.Errorf("panel width %v, want %v", got, helpPopoverW)
	}
	dismissed := 0
	shown.OnDismiss = func() { dismissed++ }
	shown.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if shown.Visible() || len(w.Canvas().Overlays().List()) != 0 || dismissed != 1 {
		t.Errorf("Esc must close it once: visible %v, overlays %d, dismissed %d", shown.Visible(), len(w.Canvas().Overlays().List()), dismissed)
	}
	shown.Dismiss()
	if dismissed != 1 {
		t.Error("a second Dismiss must do nothing")
	}
}

func TestHelpPopoverTapOutsideCloses(t *testing.T) {
	w, _ := helpWindow(t, helpFull)
	p := ShowHelp(w.Canvas(), fyne.NewPos(100, 100), helpFull)
	inside := p.panel.Position().Add(fyne.NewPos(10, 10))
	p.Tapped(&fyne.PointEvent{Position: inside})
	if !p.Visible() {
		t.Fatal("a tap inside must keep it open")
	}
	p.Tapped(&fyne.PointEvent{Position: fyne.NewPos(1, 1)})
	if p.Visible() {
		t.Fatal("a tap outside must close it")
	}
}

func TestHelpPopoverStaysInCanvas(t *testing.T) {
	w, _ := helpWindow(t, helpFull)
	p := ShowHelp(w.Canvas(), fyne.NewPos(590, 390), helpFull)
	pos, size := p.panel.Position(), p.panel.Size()
	cs := w.Canvas().Size()
	if pos.X < 0 || pos.Y < 0 || pos.X+size.Width > cs.Width || pos.Y+size.Height > cs.Height {
		t.Errorf("panel %v %v leaves the canvas %v", pos, size, cs)
	}
}

func TestHelpContentParts(t *testing.T) {
	if got := len(helpFull.parts()); got != 4 {
		t.Errorf("full content has %d parts", got)
	}
	noRisk := HelpContent{Title: "Respond to ping", What: "Answers ping.", When: "Never needed."}
	parts := noRisk.parts()
	if len(parts) != 2 || parts[0][0] != "What it does" || parts[1][0] != "When you need it" {
		t.Errorf("no-risk parts: %v", parts)
	}
	w, _ := helpWindow(t, noRisk)
	p := ShowHelp(w.Canvas(), fyne.NewPos(10, 10), noRisk)
	long := ShowHelp(w.Canvas(), fyne.NewPos(10, 10), helpFull)
	if p.PanelSize().Height >= long.PanelSize().Height {
		t.Errorf("a popover without risk parts must be shorter: %v vs %v", p.PanelSize(), long.PanelSize())
	}
}

func TestInfoButtonKeyboard(t *testing.T) {
	w, b := helpWindow(t, helpFull)
	w.Canvas().Focus(b)
	b.TypedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if len(w.Canvas().Overlays().List()) != 1 {
		t.Fatal("Space must open the popover")
	}
}

// A fresh InfoButton shows no focus ring: the ring is hidden before the first Refresh, not only
// after it.
func TestInfoButtonNoRingBeforeFirstRefresh(t *testing.T) {
	themedApp(t)
	b := NewInfoButton(helpFull)
	r := test.TempWidgetRenderer(t, b).(*infoButtonRenderer)
	if !r.ring.Hidden {
		t.Fatal("the focus ring is visible on a button without focus")
	}
	b.FocusGained()
	if r.ring.Hidden {
		t.Error("the ring must show with the focus")
	}
	b.FocusLost()
	if !r.ring.Hidden {
		t.Error("the ring must hide when the focus is lost")
	}
}
