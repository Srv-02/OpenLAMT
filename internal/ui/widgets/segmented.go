package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// SegmentedStyle selects the look of a Segmented control.
type SegmentedStyle int

const (
	// SegmentedToolbar is the KVM|Console switch of the device toolbar (Main.dc.html .segw/.sg):
	// a symbol above a caption per segment, track radius 11 with 3 px padding, the selected
	// segment white with radius 8, a light shadow and accent text. It is as high as a toolbar
	// item (style.ToolbarItemHeight).
	SegmentedToolbar SegmentedStyle = iota
	// SegmentedText is the small text switch of the settings (Settings.dc.html .seg: "DHCP |
	// Static", "None | KVM only | All"): 12 px text, track radius 7 with 2 px padding,
	// segments radius 5 with 3 x 10 px padding.
	SegmentedText
	// SegmentedTabs are the tabs of the device management sidebar (Main.dc.html .tab): segments
	// of equal width over the full width, 12 px text, track radius 9 with 3 px padding, segments
	// radius 7 and 6 px high padding. Where equal widths do not fit the longest caption (German),
	// the segments share the width in proportion to their captions.
	SegmentedTabs
)

// SegmentOption is one segment: a symbol (SegmentedToolbar only) and a caption.
type SegmentOption struct {
	Icon  icons.Name
	Label string
}

// TextOptions returns options with the given captions and no symbol.
func TextOptions(labels ...string) []SegmentOption {
	out := make([]SegmentOption, len(labels))
	for i, l := range labels {
		out[i] = SegmentOption{Label: l}
	}
	return out
}

// segGeometry holds the measures of one style.
type segGeometry struct {
	pad, gap, trackRadius, segRadius float32
	padH, padV, minSeg               float32
	textSize                         float32
}

func (s SegmentedStyle) geometry() segGeometry {
	switch s {
	case SegmentedToolbar:
		return segGeometry{pad: 3, gap: 2, trackRadius: 11, segRadius: 8, padH: 12, padV: 4, minSeg: 56,
			textSize: style.Size(style.SizeNameToolbarCaption)}
	case SegmentedTabs:
		return segGeometry{pad: 3, gap: 2, trackRadius: 9, segRadius: 7, padH: 4, padV: 6,
			textSize: style.Size(style.SizeNameSmallText)}
	}
	return segGeometry{pad: 2, gap: 0, trackRadius: 7, segRadius: 5, padH: 10, padV: 3,
		textSize: style.Size(style.SizeNameSmallText)}
}

// Segmented is a segmented control in one of three styles (SegmentedStyle). A tap on another
// segment selects it and calls OnChanged; a tap on the selected segment calls OnReselected
// (the selected KVM segment connects or disconnects KVM). SetSelected changes the selection
// without calling either. Left and Right move the selection while it has the focus, Space and
// Return count as a tap on the selected segment. Disable greys out the whole control.
type Segmented struct {
	widget.DisableableWidget

	// OnChanged is called with the new index after the user selected another segment.
	OnChanged func(int)
	// OnReselected is called with the index after the user tapped the selected segment.
	OnReselected func(int)

	style    SegmentedStyle
	options  []SegmentOption
	selected int
	focused  bool
}

var (
	_ fyne.Tappable  = (*Segmented)(nil)
	_ fyne.Focusable = (*Segmented)(nil)
)

// NewSegmented returns a control in style st with the first segment selected.
func NewSegmented(st SegmentedStyle, options []SegmentOption, onChanged func(int)) *Segmented {
	s := &Segmented{style: st, options: options, OnChanged: onChanged}
	s.ExtendBaseWidget(s)
	return s
}

// Selected returns the index of the selected segment.
func (s *Segmented) Selected() int { return s.selected }

// SetSelected selects segment i without calling OnChanged or OnReselected; an index out of
// range is ignored.
func (s *Segmented) SetSelected(i int) {
	if i < 0 || i >= len(s.options) || i == s.selected {
		return
	}
	s.selected = i
	s.Refresh()
}

// SetLabel changes the caption of segment i.
func (s *Segmented) SetLabel(i int, label string) {
	if i < 0 || i >= len(s.options) {
		return
	}
	s.options[i].Label = label
	s.Refresh()
}

// Tap acts like a tap of the user on segment i: OnChanged for another segment, OnReselected
// for the selected one, nothing while disabled.
func (s *Segmented) Tap(i int) {
	if s.Disabled() || i < 0 || i >= len(s.options) {
		return
	}
	if i == s.selected {
		if s.OnReselected != nil {
			s.OnReselected(i)
		}
		return
	}
	s.selected = i
	s.Refresh()
	if s.OnChanged != nil {
		s.OnChanged(i)
	}
}

func (s *Segmented) Tapped(e *fyne.PointEvent) {
	if s.Disabled() {
		return
	}
	for i := range s.options {
		b := s.segmentBounds(i)
		if e.Position.X >= b.pos.X && e.Position.X < b.pos.X+b.size.Width+s.style.geometry().gap {
			s.Tap(i)
			return
		}
	}
}

func (s *Segmented) FocusGained() { s.focused = true; s.Refresh() }
func (s *Segmented) FocusLost()   { s.focused = false; s.Refresh() }
func (s *Segmented) TypedRune(r rune) {
	if r == ' ' {
		s.Tap(s.selected)
	}
}
func (s *Segmented) TypedKey(e *fyne.KeyEvent) {
	switch e.Name {
	case fyne.KeyLeft:
		if s.selected > 0 {
			s.Tap(s.selected - 1)
		}
	case fyne.KeyRight:
		s.Tap(s.selected + 1)
	case fyne.KeyReturn, fyne.KeyEnter:
		s.Tap(s.selected)
	}
}

// segBounds is the rectangle of one segment inside the widget.
type segBounds struct {
	pos  fyne.Position
	size fyne.Size
}

// contentSize is the size of a segment's symbol and caption.
func (s *Segmented) contentSize(o SegmentOption) fyne.Size {
	g := s.style.geometry()
	ts := fyne.MeasureText(o.Label, g.textSize, fyne.TextStyle{})
	if s.style == SegmentedToolbar {
		return fyne.NewSize(max(ts.Width, style.ToolbarIconSize), tbIconRow+tbGap+ts.Height)
	}
	return ts
}

// segmentWidths returns the minimum width of every segment.
func (s *Segmented) segmentWidths() []float32 {
	g := s.style.geometry()
	out := make([]float32, len(s.options))
	for i, o := range s.options {
		out[i] = max(g.minSeg, s.contentSize(o).Width+2*g.padH)
	}
	return out
}

// frame is the width of the track padding and the gaps between the segments.
func (s *Segmented) frame() float32 {
	g := s.style.geometry()
	return 2*g.pad + g.gap*float32(max(0, len(s.options)-1))
}

// layoutWidths returns the width of every segment at the current size: the extra width shared
// equally; tabs of equal width while the widest caption fits, else in proportion to their
// captions.
func (s *Segmented) layoutWidths() []float32 {
	widths := s.segmentWidths()
	room := s.Size().Width - s.frame()
	var sum, widest float32
	for _, w := range widths {
		sum += w
		widest = max(widest, w)
	}
	n := float32(max(1, len(widths)))
	switch {
	case s.style == SegmentedTabs && widest*n <= room:
		for i := range widths {
			widths[i] = room / n
		}
	case s.style == SegmentedTabs && sum > 0:
		for i, w := range widths {
			widths[i] = max(0, room) * w / sum
		}
	default:
		extra := max(0, room-sum) / n
		for i := range widths {
			widths[i] += extra
		}
	}
	return widths
}

// trackHeight is the height of the track.
func (s *Segmented) trackHeight() float32 {
	if s.style == SegmentedToolbar {
		return style.ToolbarItemHeight
	}
	g := s.style.geometry()
	var h float32
	for _, o := range s.options {
		h = max(h, s.contentSize(o).Height)
	}
	return h + 2*g.padV + 2*g.pad
}

func (s *Segmented) minSize() fyne.Size {
	w := s.frame()
	for _, sw := range s.segmentWidths() {
		w += sw
	}
	return fyne.NewSize(w, s.trackHeight())
}

// segmentBounds returns the rectangle of segment i for the current size (layoutWidths).
func (s *Segmented) segmentBounds(i int) segBounds {
	g := s.style.geometry()
	size := s.Size()
	widths := s.layoutWidths()
	th := s.trackHeight()
	top := (max(size.Height, th)-th)/2 + g.pad
	x := g.pad
	for j := 0; j < i; j++ {
		x += widths[j] + g.gap
	}
	return segBounds{fyne.NewPos(x, top), fyne.NewSize(widths[i], th-2*g.pad)}
}

func (s *Segmented) CreateRenderer() fyne.WidgetRenderer {
	g := s.style.geometry()
	r := &segmentedRenderer{s: s,
		track: &canvas.Rectangle{CornerRadius: g.trackRadius},
		focus: &canvas.Rectangle{CornerRadius: g.trackRadius + 2, StrokeWidth: 2, FillColor: color.Transparent},
	}
	r.Refresh()
	return r
}

type segmentParts struct {
	shadow, bg *canvas.Rectangle
	icon       *Symbol
	text       *canvas.Text
}

type segmentedRenderer struct {
	s            *Segmented
	track, focus *canvas.Rectangle
	segs         []segmentParts
	objects      []fyne.CanvasObject
}

func (r *segmentedRenderer) MinSize() fyne.Size { return r.s.minSize() }

// sync creates the parts of every segment once.
func (r *segmentedRenderer) sync() {
	if len(r.segs) == len(r.s.options) {
		return
	}
	g := r.s.style.geometry()
	r.segs = make([]segmentParts, len(r.s.options))
	r.objects = []fyne.CanvasObject{r.focus, r.track}
	for i := range r.segs {
		p := segmentParts{
			shadow: &canvas.Rectangle{CornerRadius: g.segRadius},
			bg:     &canvas.Rectangle{CornerRadius: g.segRadius},
			icon:   NewSymbol(nil, style.ToolbarIconSize),
			text:   newText("", g.textSize, nil, false),
		}
		r.segs[i] = p
		r.objects = append(r.objects, p.shadow, p.bg, p.icon, p.text)
	}
}

func (r *segmentedRenderer) Layout(size fyne.Size) {
	r.sync()
	s := r.s
	th := s.trackHeight()
	top := (max(size.Height, th) - th) / 2
	r.track.Move(fyne.NewPos(0, top))
	r.track.Resize(fyne.NewSize(size.Width, th))
	r.focus.Move(fyne.NewPos(-2, top-2))
	r.focus.Resize(fyne.NewSize(size.Width+4, th+4))
	for i, p := range r.segs {
		b := s.segmentBounds(i)
		p.bg.Move(b.pos)
		p.bg.Resize(b.size)
		p.shadow.Move(b.pos.AddXY(0, 1))
		p.shadow.Resize(b.size)
		cs := s.contentSize(s.options[i])
		ts := p.text.MinSize()
		y := b.pos.Y + (b.size.Height-cs.Height)/2
		if s.style == SegmentedToolbar {
			p.icon.Move(fyne.NewPos(b.pos.X+(b.size.Width-style.ToolbarIconSize)/2, y+(tbIconRow-style.ToolbarIconSize)/2))
			p.icon.Resize(fyne.NewSquareSize(style.ToolbarIconSize))
			y += tbIconRow + tbGap
		}
		p.text.Move(fyne.NewPos(b.pos.X+(b.size.Width-ts.Width)/2, y))
		p.text.Resize(ts)
	}
}

func (r *segmentedRenderer) Refresh() {
	r.sync()
	s := r.s
	r.track.FillColor = style.Color(style.ColorNameSegment)
	r.track.Refresh()
	r.focus.StrokeColor = color.Transparent
	if s.focused && !s.Disabled() {
		r.focus.StrokeColor = style.Color("focus")
	}
	r.focus.Refresh()
	for i, p := range r.segs {
		on := i == s.selected
		p.bg.FillColor = style.Color(style.ColorNameSegmentActive)
		p.bg.Hidden = !on
		p.bg.Refresh()
		p.shadow.FillColor = style.Color(style.ColorNameSegmentShadow)
		p.shadow.Hidden = !on
		p.shadow.Refresh()

		text, variant := style.Color(style.ColorNameControlText), icons.Toolbar
		switch {
		case s.Disabled():
			text, variant = style.Color("disabled"), icons.Disabled
		case on && s.style == SegmentedToolbar:
			text, variant = style.Color(style.ColorNameAccentText), icons.AccentText
		case on:
			text = style.Color("foreground")
		case s.style == SegmentedToolbar:
			text = style.Color(style.ColorNameTextSecondary)
		}
		p.text.Text = s.options[i].Label
		p.text.Color = text
		p.text.Refresh()
		if s.style == SegmentedToolbar && s.options[i].Icon != "" {
			p.icon.SetResource(icons.Get(s.options[i].Icon, variant))
			setVisible(p.icon, true)
		} else {
			setVisible(p.icon, false)
		}
	}
	r.Layout(s.Size())
	canvas.Refresh(s)
}

func (r *segmentedRenderer) Objects() []fyne.CanvasObject { r.sync(); return r.objects }
func (r *segmentedRenderer) Destroy()                     {}
