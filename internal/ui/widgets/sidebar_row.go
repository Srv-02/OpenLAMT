package widgets

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of a sidebar row (ManagerA.html): padding 7 x 10, gap 10, a 30 px tile with a
// 17 px symbol, name and host 1 px apart, dot on the right.
const (
	srGap      float32 = 10
	srIconSize float32 = 17
	srLineGap  float32 = 1
)

// SidebarRow is one favorite in the manager's sidebar: icon tile, name (semibold), host
// (secondary) and a status dot. Selected rows have the accent background and white text.
// The primary MouseDown selects (OnTapped) at once: Fyne's GLFW driver holds back Tapped of
// a DoubleTappable until the double-click interval has passed, which made switching lag.
// Tapped selects only when no MouseDown came before it (touch, accessibility). Double tap
// connects (OnDoubleTapped).
type SidebarRow struct {
	widget.BaseWidget

	OnTapped, OnDoubleTapped func()

	name, host   string
	icon         icons.Name
	selected     bool
	hovered      bool
	downSelected bool      // MouseDown already selected; the Tapped of the same click is skipped
	upAt         time.Time // release of that press (zero while it is held)
	dot          *StatusDot
}

var (
	_ fyne.Tappable       = (*SidebarRow)(nil)
	_ fyne.DoubleTappable = (*SidebarRow)(nil)
	_ desktop.Hoverable   = (*SidebarRow)(nil)
	_ desktop.Mouseable   = (*SidebarRow)(nil)
)

// tapGrace is how long after the release a Tapped still belongs to that press: the GLFW
// driver delays it by the double-click interval. Later ones come from a new input.
const tapGrace = time.Second

// NewSidebarRow returns a row for name and host with the server symbol and no dot.
func NewSidebarRow(name, host string) *SidebarRow {
	r := &SidebarRow{name: name, host: host, icon: icons.Server, dot: NewStatusDot(DotNone, style.StatusDotSize)}
	r.ExtendBaseWidget(r)
	return r
}

// Set changes name and host, e.g. when a list recycles the row.
func (r *SidebarRow) Set(name, host string) {
	r.name, r.host = name, host
	r.Refresh()
}

// Name and Host return the shown texts.
func (r *SidebarRow) Name() string { return r.name }
func (r *SidebarRow) Host() string { return r.host }

// SetSelected switches the accent background on or off.
func (r *SidebarRow) SetSelected(sel bool) {
	r.selected = sel
	r.dot.SetSelected(sel)
	r.Refresh()
}

// Selected reports the selection.
func (r *SidebarRow) Selected() bool { return r.selected }

// SetDot sets the status dot.
func (r *SidebarRow) SetDot(s DotState) { r.dot.SetState(s) }

// Dot returns the status dot state.
func (r *SidebarRow) Dot() DotState { return r.dot.State() }

// MouseDown selects on the primary button; other buttons (context menu) do not.
func (r *SidebarRow) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	r.downSelected, r.upAt = true, time.Time{}
	if r.OnTapped != nil {
		r.OnTapped()
	}
}

func (r *SidebarRow) MouseUp(e *desktop.MouseEvent) {
	if r.downSelected && e.Button == desktop.MouseButtonPrimary {
		r.upAt = time.Now()
	}
}

func (r *SidebarRow) Tapped(*fyne.PointEvent) {
	mine := r.downSelected && (r.upAt.IsZero() || time.Since(r.upAt) < tapGrace)
	r.downSelected = false
	if mine { // MouseDown of this click already selected
		return
	}
	if r.OnTapped != nil {
		r.OnTapped()
	}
}

func (r *SidebarRow) DoubleTapped(*fyne.PointEvent) {
	r.downSelected = false // the double click ends here, no Tapped follows
	if r.OnDoubleTapped != nil {
		r.OnDoubleTapped()
	}
}

func (r *SidebarRow) MouseIn(*desktop.MouseEvent)    { r.hovered = true; r.Refresh() }
func (r *SidebarRow) MouseMoved(*desktop.MouseEvent) {}
func (r *SidebarRow) MouseOut()                      { r.hovered = false; r.Refresh() }

func (r *SidebarRow) CreateRenderer() fyne.WidgetRenderer {
	sr := &sidebarRowRenderer{
		r:    r,
		bg:   &canvas.Rectangle{CornerRadius: style.SidebarRowRadius},
		tile: &canvas.Rectangle{CornerRadius: style.SidebarTileRadius},
		icon: NewSymbol(nil, 0),
		name: newText("", style.Size("text"), nil, true),
		host: newText("", style.Size(style.SizeNameSecondaryText), nil, false),
	}
	sr.Refresh()
	return sr
}

type sidebarRowRenderer struct {
	r          *SidebarRow
	bg, tile   *canvas.Rectangle
	icon       *Symbol
	name, host *canvas.Text
}

func (s *sidebarRowRenderer) textHeight() float32 {
	return s.name.MinSize().Height + srLineGap + s.host.MinSize().Height
}

func (s *sidebarRowRenderer) MinSize() fyne.Size {
	h := max(style.SidebarTileSize, s.textHeight()) + 2*style.SidebarRowPadV
	w := 2*style.SidebarRowPadH + style.SidebarTileSize + 2*srGap + s.r.dot.MinSize().Width + 40
	return fyne.NewSize(w, h)
}

func (s *sidebarRowRenderer) Layout(size fyne.Size) {
	s.bg.Resize(size)
	x := style.SidebarRowPadH
	ts := style.SidebarTileSize
	s.tile.Move(fyne.NewPos(x, (size.Height-ts)/2))
	s.tile.Resize(fyne.NewSquareSize(ts))
	s.icon.Move(fyne.NewPos(x+(ts-srIconSize)/2, (size.Height-srIconSize)/2))
	s.icon.Resize(fyne.NewSquareSize(srIconSize))
	x += ts + srGap

	d := s.r.dot
	ds := d.MinSize()
	right := size.Width - style.SidebarRowPadH
	d.Move(fyne.NewPos(right-ds.Width, (size.Height-ds.Height)/2))
	d.Resize(ds)
	textW := right - ds.Width - srGap - x

	fitText(s.name, s.r.name, textW)
	fitText(s.host, s.r.host, textW)
	y := (size.Height - s.textHeight()) / 2
	nh := s.name.MinSize().Height
	s.name.Move(fyne.NewPos(x, y))
	s.name.Resize(fyne.NewSize(textW, nh))
	s.host.Move(fyne.NewPos(x, y+nh+srLineGap))
	s.host.Resize(fyne.NewSize(textW, s.host.MinSize().Height))
}

func (s *sidebarRowRenderer) Refresh() {
	r := s.r
	var bg color.Color = color.Transparent
	tile := style.Color(style.ColorNameTile)
	variant := icons.Muted
	nameColor := style.Color("foreground")
	hostColor := style.Color(style.ColorNameSectionLabel)
	switch {
	case r.selected:
		bg = style.Color("primary")
		tile = style.Color(style.ColorNameTileSelected)
		variant = icons.White
		nameColor = style.Color(style.ColorNameWhite)
		hostColor = style.Color(style.ColorNameSelectedText2)
	case r.hovered:
		bg = style.Color("hover")
	}
	s.bg.FillColor = bg
	s.bg.Refresh()
	s.tile.FillColor = tile
	s.tile.Refresh()
	if res := icons.Get(r.icon, variant); s.icon.Resource() != res { // rasterizing is costly
		s.icon.SetResource(res)
	}
	s.name.Color = nameColor
	s.host.Color = hostColor
	s.name.Text, s.host.Text = r.name, r.host // fitText shortens them in Layout
	s.Layout(r.Size())
	s.name.Refresh()
	s.host.Refresh()
}

func (s *sidebarRowRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{s.bg, s.tile, s.icon, s.name, s.host, s.r.dot}
}

func (s *sidebarRowRenderer) Destroy() {}
