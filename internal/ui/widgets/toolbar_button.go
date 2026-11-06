package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Geometry of a toolbar item (SessionA.html): 52 high, 8 horizontal padding, radius 8, a 24 px
// icon row with a 20 px symbol, 3 px gap, then the caption.
const (
	tbPadH        float32 = 8
	tbRadius      float32 = 8
	tbIconRow     float32 = 24
	tbGap         float32 = 3
	tbChevron     float32 = 10
	tbBadgeH      float32 = 18
	tbBadgePadH   float32 = 5
	tbBadgeText   float32 = 10
	tbDotLeft     float32 = 15 // dot position relative to the icon's top left corner
	tbDotTop      float32 = 1
	chevronStroke float32 = 2.4 // design stroke of the chevron, in units of the 24 px grid
)

// ToolbarButton is an item of the session toolbar: a symbol above a caption.
//
// States: normal, hover, open (menu shown: pressed background, white symbol), active (toggled,
// e.g. KVM connected: accent symbol on a light background) and disabled (35 % opacity).
// Optional: a chevron for menu buttons, a status dot on the symbol (Power), and a badge text
// instead of the symbol (Quality "HQ"). Space and Return trigger it when focused.
type ToolbarButton struct {
	widget.DisableableWidget

	OnTapped func()

	icon    icons.Name
	caption string
	badge   string
	chevron bool
	active  bool
	open    bool
	hovered bool
	focused bool
	dot     *StatusDot // nil without dot
}

var (
	_ fyne.Tappable     = (*ToolbarButton)(nil)
	_ fyne.Focusable    = (*ToolbarButton)(nil)
	_ desktop.Hoverable = (*ToolbarButton)(nil)
)

// NewToolbarButton returns a button with symbol icon and caption.
func NewToolbarButton(icon icons.Name, caption string, tapped func()) *ToolbarButton {
	b := &ToolbarButton{icon: icon, caption: caption, OnTapped: tapped}
	b.ExtendBaseWidget(b)
	return b
}

// NewToolbarMenuButton returns a button with a chevron next to the symbol, for menus.
func NewToolbarMenuButton(icon icons.Name, caption string, tapped func()) *ToolbarButton {
	b := NewToolbarButton(icon, caption, tapped)
	b.chevron = true
	return b
}

// SetCaption changes the caption.
func (b *ToolbarButton) SetCaption(s string) { b.caption = s; b.Refresh() }

// Caption returns the caption.
func (b *ToolbarButton) Caption() string { return b.caption }

// SetIcon changes the symbol.
func (b *ToolbarButton) SetIcon(n icons.Name) { b.icon = n; b.Refresh() }

// SetBadge shows s (e.g. "HQ") in a bordered box instead of the symbol; "" shows the symbol.
func (b *ToolbarButton) SetBadge(s string) { b.badge = s; b.Refresh() }

// SetActive marks the button toggled on (KVM connected).
func (b *ToolbarButton) SetActive(on bool) { b.active = on; b.Refresh() }

// Active reports the toggled state.
func (b *ToolbarButton) Active() bool { return b.active }

// SetOpen marks the button pressed while its menu is shown.
func (b *ToolbarButton) SetOpen(on bool) { b.open = on; b.Refresh() }

// SetDot shows a status dot on the symbol; DotNone removes it. Its ring has the color of the
// item background (SessionA.html #2A2A2C, open menu SessionA2.html #3A3A3C), so it cuts the dot
// out of the symbol under it.
func (b *ToolbarButton) SetDot(s DotState) {
	if b.dot == nil {
		b.dot = NewStatusDot(DotNone, style.StatusDotSizeSmall)
		b.dot.SetBorder(style.Color(style.ColorNameToolbar))
	}
	b.dot.SetState(s)
	b.Refresh()
}

// Dot returns the shown dot state.
func (b *ToolbarButton) Dot() DotState {
	if b.dot == nil {
		return DotNone
	}
	return b.dot.State()
}

func (b *ToolbarButton) Tapped(*fyne.PointEvent) {
	if b.Disabled() || b.OnTapped == nil {
		return
	}
	b.OnTapped()
}

func (b *ToolbarButton) MouseIn(*desktop.MouseEvent)    { b.hovered = true; b.Refresh() }
func (b *ToolbarButton) MouseMoved(*desktop.MouseEvent) {}
func (b *ToolbarButton) MouseOut()                      { b.hovered = false; b.Refresh() }

func (b *ToolbarButton) FocusGained() { b.focused = true; b.Refresh() }
func (b *ToolbarButton) FocusLost()   { b.focused = false; b.Refresh() }
func (b *ToolbarButton) TypedRune(r rune) {
	if r == ' ' {
		b.Tapped(nil)
	}
}
func (b *ToolbarButton) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeyReturn || e.Name == fyne.KeyEnter {
		b.Tapped(nil)
	}
}

// background returns the item background for the current state.
func (b *ToolbarButton) background() color.Color {
	switch {
	case b.Disabled():
		return color.Transparent
	case b.open:
		return style.Color("pressed")
	case b.active, b.hovered, b.focused:
		return style.Color(style.ColorNameActive)
	}
	return color.Transparent
}

// iconVariant returns the symbol color for the current state.
func (b *ToolbarButton) iconVariant() icons.Variant {
	switch {
	case b.active && !b.Disabled():
		return icons.Accent
	case b.open:
		return icons.MenuOpen
	}
	return icons.Toolbar
}

func (b *ToolbarButton) CreateRenderer() fyne.WidgetRenderer {
	r := &toolbarButtonRenderer{
		b:        b,
		bg:       &canvas.Rectangle{CornerRadius: tbRadius},
		icon:     NewSymbol(nil, 0),
		chevron:  NewSymbol(icons.GetStroke(icons.ChevronDown, icons.Secondary, chevronStroke), 0),
		badgeBox: &canvas.Rectangle{CornerRadius: 4, StrokeWidth: 1.5, FillColor: color.Transparent},
		badge:    newText("", tbBadgeText, nil, true),
		caption:  newText("", style.Size(style.SizeNameToolbarCaption), nil, false),
	}
	r.Refresh()
	return r
}

type toolbarButtonRenderer struct {
	b        *ToolbarButton
	bg       *canvas.Rectangle
	icon     *Symbol
	chevron  *Symbol
	badgeBox *canvas.Rectangle
	badge    *canvas.Text
	caption  *canvas.Text
}

// glyphSize is the size of the symbol (or badge) part of the icon row.
func (r *toolbarButtonRenderer) glyphSize() fyne.Size {
	if r.b.badge != "" {
		w := fyne.MeasureText(r.b.badge, tbBadgeText, fyne.TextStyle{Bold: true}).Width + 2*tbBadgePadH
		return fyne.NewSize(w, tbBadgeH)
	}
	return fyne.NewSquareSize(style.ToolbarIconSize)
}

func (r *toolbarButtonRenderer) iconRowWidth() float32 {
	w := r.glyphSize().Width
	if r.b.chevron {
		w += tbGap + tbChevron
	}
	return w
}

func (r *toolbarButtonRenderer) MinSize() fyne.Size {
	capW := fyne.MeasureText(r.b.caption, r.caption.TextSize, r.caption.TextStyle).Width
	return fyne.NewSize(max(capW, r.iconRowWidth())+2*tbPadH, style.ToolbarItemHeight)
}

func (r *toolbarButtonRenderer) Layout(size fyne.Size) {
	h := style.ToolbarItemHeight
	top := (size.Height - h) / 2
	r.bg.Move(fyne.NewPos(0, top))
	r.bg.Resize(fyne.NewSize(size.Width, h))

	capSize := r.caption.MinSize()
	content := tbIconRow + tbGap + capSize.Height
	y := top + (h-content)/2

	g := r.glyphSize()
	x := (size.Width - r.iconRowWidth()) / 2
	gy := y + (tbIconRow-g.Height)/2
	r.icon.Move(fyne.NewPos(x, gy))
	r.icon.Resize(g)
	r.badgeBox.Move(fyne.NewPos(x, gy))
	r.badgeBox.Resize(g)
	bs := r.badge.MinSize()
	r.badge.Move(fyne.NewPos(x+(g.Width-bs.Width)/2, gy+(g.Height-bs.Height)/2))
	r.badge.Resize(bs)
	r.chevron.Move(fyne.NewPos(x+g.Width+tbGap, y+(tbIconRow-tbChevron)/2))
	r.chevron.Resize(fyne.NewSquareSize(tbChevron))
	if d := r.b.dot; d != nil {
		d.Move(fyne.NewPos(x+tbDotLeft, y+tbDotTop))
		d.Resize(d.MinSize())
	}

	r.caption.Move(fyne.NewPos((size.Width-capSize.Width)/2, y+tbIconRow+tbGap))
	r.caption.Resize(capSize)
}

func (r *toolbarButtonRenderer) Refresh() {
	b := r.b
	r.bg.FillColor = b.background()
	r.bg.Refresh()

	translucency := 0.0
	if b.Disabled() {
		translucency = 1 - style.DisabledOpacity
	}
	r.icon.Translucency = translucency
	r.icon.SetResource(icons.Get(b.icon, r.b.iconVariant()))
	setVisible(r.icon, b.badge == "")
	r.chevron.Translucency = translucency
	r.chevron.Refresh()
	setVisible(r.chevron, b.chevron)

	bar := style.Color(style.ColorNameToolbar)
	fg := icons.Color(b.iconVariant())
	capColor := style.Color(style.ColorNameTextSecondary)
	if b.open || b.active {
		capColor = style.Color(style.ColorNameToolbarIcon)
	}
	var badgeColor color.Color = fg
	if b.Disabled() {
		capColor = style.Over(capColor, style.DisabledOpacity, bar)
		badgeColor = style.Over(fg, style.DisabledOpacity, bar)
	}
	r.badgeBox.Hidden = b.badge == ""
	r.badgeBox.StrokeColor = badgeColor
	r.badgeBox.Refresh()
	r.badge.Hidden = b.badge == ""
	r.badge.Text = b.badge
	r.badge.Color = badgeColor
	r.badge.Refresh()

	r.caption.Text = b.caption
	r.caption.Color = capColor
	r.caption.Refresh()
	if b.dot != nil {
		b.dot.SetBorder(style.Over(r.bg.FillColor, 1, bar))
	}
	r.Layout(b.Size())
	canvas.Refresh(b)
}

func (r *toolbarButtonRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.bg, r.icon, r.badgeBox, r.badge, r.chevron, r.caption}
	if r.b.dot != nil {
		objs = append(objs, r.b.dot)
	}
	return objs
}

func (r *toolbarButtonRenderer) Destroy() {}
