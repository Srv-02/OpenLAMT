package widgets

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// moreOptions is the sidebar's five tabs plus the natural-width "More" segment with a chevron.
func moreOptions() []SegmentOption {
	o := TextOptions("Hardware", "Events", "Audit", "Settings", "Users")
	return append(o, SegmentOption{Label: "More", Icon: icons.ChevronDown, Natural: true})
}

// A natural segment keeps its minimum width (caption, symbol, padding); the others share the
// rest.
func TestSegmentedTabsNaturalSegment(t *testing.T) {
	themedApp(t)
	s := NewSegmented(SegmentedTabs, moreOptions(), nil)
	test.NewTempWindow(t, s)
	s.Resize(fyne.NewSize(368, s.MinSize().Height))
	g := SegmentedTabs.geometry()
	if s.Len() != 6 || s.Label(5) != "More" {
		t.Fatalf("len %d last %q", s.Len(), s.Label(5))
	}
	want := fyne.MeasureText("More", g.textSize, fyne.TextStyle{}).Width + trailingGap + trailingSize + 2*g.padH
	more := s.segmentBounds(5)
	if d := more.size.Width - want; d < -0.5 || d > 0.5 {
		t.Errorf("More is %v wide, want its minimum %v", more.size.Width, want)
	}
	mins := s.segmentWidths()
	for i := 0; i < 5; i++ { // the others share the rest, none narrower than its caption
		if w := s.segmentBounds(i).size.Width; w < mins[i]-0.5 {
			t.Errorf("segment %d is %v wide, its caption needs %v", i, w, mins[i])
		}
	}
	if end := more.pos.X + more.size.Width; end < 368-3-0.5 || end > 368-3+0.5 {
		t.Errorf("segments end at %v, want 365", end)
	}
}

// Without a natural segment the widths are the old ones: equal while the widest fits.
func TestSegmentedTabsWithoutNaturalKeepWidths(t *testing.T) {
	themedApp(t)
	s := NewSegmented(SegmentedTabs, TextOptions("Hardware", "Events", "Audit", "Settings", "Users"), nil)
	test.NewTempWindow(t, s)
	s.Resize(fyne.NewSize(368, s.MinSize().Height))
	want := (368 - s.frame()) / 5
	for i := 0; i < 5; i++ {
		if w := s.segmentBounds(i).size.Width; w != want {
			t.Errorf("segment %d is %v wide, want %v", i, w, want)
		}
	}
}

// The caption and the trailing symbol are centered as one unit, 3 px apart; the symbol is
// 14 px and takes the variant of the segment state.
func TestSegmentedTabsTrailingSymbol(t *testing.T) {
	themedApp(t)
	s := NewSegmented(SegmentedTabs, moreOptions(), nil)
	test.NewTempWindow(t, s)
	s.Resize(fyne.NewSize(368, s.MinSize().Height))
	r := test.WidgetRenderer(s).(*segmentedRenderer)
	p, b := r.segs[5], s.segmentBounds(5)
	if !p.icon.Visible() || p.icon.Size() != fyne.NewSquareSize(14) || p.icon.Resource() != icons.Get(icons.ChevronDown, icons.Toolbar) {
		t.Fatalf("symbol visible %v size %v", p.icon.Visible(), p.icon.Size())
	}
	if gap := p.icon.Position().X - (p.text.Position().X + p.text.Size().Width); gap < 2.5 || gap > 3.5 {
		t.Errorf("gap %v, want 3", gap)
	}
	left := p.text.Position().X - b.pos.X
	right := b.pos.X + b.size.Width - (p.icon.Position().X + 14)
	if d := left - right; d < -0.5 || d > 0.5 {
		t.Errorf("content not centered: %v left, %v right", left, right)
	}
	if r.segs[0].icon.Visible() {
		t.Error("a segment without Icon shows a symbol")
	}
	s.SetSelected(5)
	if r.segs[5].icon.Resource() != icons.Get(icons.ChevronDown, icons.Foreground) {
		t.Error("selected symbol is not the foreground variant")
	}
	s.Disable()
	if r.segs[5].icon.Resource() != icons.Get(icons.ChevronDown, icons.Disabled) {
		t.Error("disabled symbol is not the disabled variant")
	}
}

// germanTabs are the German sidebar captions with a natural "More" caption of the given text.
func germanTabs(more string) []SegmentOption {
	o := TextOptions("Hardware", "Ereignisse", "Audit", "Einstellungen", "Benutzer")
	return append(o, SegmentOption{Label: more, Icon: icons.ChevronDown, Natural: true})
}

// A long natural caption takes only what the other tabs leave: no tab is narrower than its
// caption, the natural one is drawn with an ellipsis and keeps its label in the options.
func TestSegmentedTabsNaturalCaptionEllipsized(t *testing.T) {
	themedApp(t)
	const full = "System Defense"
	s := NewSegmented(SegmentedTabs, germanTabs(full), nil)
	test.NewTempWindow(t, s)
	s.Resize(fyne.NewSize(367+s.frame(), s.MinSize().Height))
	mins := s.segmentWidths()
	var sum float32
	for i := 0; i < 5; i++ {
		w := s.segmentBounds(i).size.Width
		sum += w
		if w < mins[i]-0.5 {
			t.Errorf("segment %d is %v wide, its caption needs %v", i, w, mins[i])
		}
	}
	more := s.segmentBounds(5)
	if more.size.Width >= mins[5] {
		t.Errorf("natural segment keeps %v, minimum %v", more.size.Width, mins[5])
	}
	if end := more.pos.X + more.size.Width; end > 367+s.frame()-3+0.5 {
		t.Errorf("segments end at %v, beyond the track", end)
	}
	r := test.WidgetRenderer(s).(*segmentedRenderer)
	p := r.segs[5]
	if p.text.Text == full || !strings.HasSuffix(p.text.Text, "…") || s.Label(5) != full {
		t.Fatalf("drawn %q, label %q", p.text.Text, s.Label(5))
	}
	if w := p.text.MinSize().Width + trailingGap + trailingSize; w > more.size.Width-2*SegmentedTabs.geometry().padH+0.5 {
		t.Errorf("caption and symbol need %v, segment has %v", w, more.size.Width)
	}
	if p.icon.Position().X+14 > more.pos.X+more.size.Width {
		t.Error("symbol leaves the segment")
	}
	if r.segs[3].text.Text != "Einstellungen" {
		t.Errorf("a tab was shortened: %q", r.segs[3].text.Text)
	}
}

// A short natural caption fits and stays unchanged.
func TestSegmentedTabsNaturalCaptionFits(t *testing.T) {
	themedApp(t)
	s := NewSegmented(SegmentedTabs, germanTabs("Mehr"), nil)
	test.NewTempWindow(t, s)
	s.Resize(fyne.NewSize(367+s.frame(), s.MinSize().Height))
	g := SegmentedTabs.geometry()
	want := fyne.MeasureText("Mehr", g.textSize, fyne.TextStyle{}).Width + trailingGap + trailingSize + 2*g.padH
	if w := s.segmentBounds(5).size.Width; w < want-0.5 || w > want+0.5 {
		t.Errorf("natural segment %v, want %v", w, want)
	}
	if got := test.WidgetRenderer(s).(*segmentedRenderer).segs[5].text.Text; got != "Mehr" {
		t.Errorf("drawn %q", got)
	}
}
